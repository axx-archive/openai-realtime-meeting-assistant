// END-TO-END multi-party WebRTC verification of the 2026-06-16 media fixes,
// through the REAL Go SFU, in REAL browser engines (Chromium + WebKit/Safari),
// with getUserMedia/getDisplayMedia overridden to live canvas.captureStream()
// MediaStreams (no physical camera/phone/screen needed, but the real WebRTC +
// SFU + render paths all run). Reproduces what unit tests can't:
//   [1] a real WebRTC remote track rendered in the Safari engine starts as one
//       muted combined A/V owner, promotes that exact owner on a trusted
//       gesture, and shows ZERO same-track srcObject reattaches over 36s;
//   [2] an iPhone-13-sized mobile peer joins portrait → detected mobile, capture
//       has no landscape aspectRatio pin, zero applyConstraints (no camera
//       restart), and existing landscape feeds do NOT flip to portrait;
//   [3] a shared screen reaches BOTH a Safari-engine peer and an iPhone peer on
//       the presentation stage.
//
// Run (uses the repo-pinned Playwright; NOT in `go test`):
//   # 1) start an isolated local instance (separate data dir):
//   go build -o /tmp/ma . && rm -rf /tmp/ma-e2e && mkdir -p /tmp/ma-e2e/data
//   MEETING_ROOM_PASSWORD="smoke-pass-1234" \
//     MEETING_MEMORY_PATH=/tmp/ma-e2e/data/meeting-memory.jsonl /tmp/ma -addr :3191 &
//   # 2) from the repository root:
//   npm install
//   npm run media:browsers
//   npm run media:e2e
// Override MEDIA_FIX_E2E_BASE / MEDIA_FIX_E2E_PASSWORD for another host.
// MEDIA_FIX_E2E_ROOM_NAME creates and automatically archives a dedicated room,
// keeping the default Office untouched. MEDIA_FIX_E2E_CHROME_PATH avoids a
// shared Playwright-browser cache when a system Chrome is available.
// Seeded accounts (accounts.go) all share MEETING_ROOM_PASSWORD locally.
import { webkit, chromium, devices, request } from 'playwright'

const BASE = process.env.MEDIA_FIX_E2E_BASE || 'http://localhost:3191'
const PW = process.env.MEDIA_FIX_E2E_PASSWORD || 'smoke-pass-1234'
const ROOM_NAME = String(process.env.MEDIA_FIX_E2E_ROOM_NAME || '').trim()
const CHROME_PATH = String(process.env.MEDIA_FIX_E2E_CHROME_PATH || '').trim()
const JOIN_TIMEOUT_MS = Math.max(35000, Number(process.env.MEDIA_FIX_E2E_JOIN_TIMEOUT_MS) || 35000)
let pass = 0, fail = 0
const ok = (n,c)=>{ if(c){pass++;console.log('  PASS',n)}else{fail++;console.log('  FAIL',n)} }
const sleep = ms => new Promise(r=>setTimeout(r,ms))
let roomControl = null

async function withTimeout(promise, timeoutMs, message) {
  let timer
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(message)), timeoutMs)
  })
  try {
    return await Promise.race([promise, timeout])
  } finally {
    clearTimeout(timer)
  }
}

async function createIsolatedRoom() {
  if (!ROOM_NAME) return null
  const context = await request.newContext({
    baseURL: BASE,
    extraHTTPHeaders: { Origin: new URL(BASE).origin },
    timeout: 15000
  })
  try {
    const login = await context.post('/auth/login', { data: { name: 'AJ', password: PW } })
    if (!login.ok()) throw new Error(`test-room login failed: ${login.status()}`)
    const response = await context.post('/rooms', {
      data: { name: ROOM_NAME, passcode: '', guestAccess: false }
    })
    if (!response.ok()) throw new Error(`test-room create failed: ${response.status()}`)
    const payload = await response.json()
    const id = String(payload?.room?.id || '').trim()
    if (!id) throw new Error('test-room create returned no room id')
    return { context, id }
  } catch (error) {
    await context.dispose().catch(()=>{})
    throw error
  }
}

async function archiveIsolatedRoom(control) {
  if (!control) return
  try {
    const response = await control.context.post(`/rooms/${encodeURIComponent(control.id)}/archive`, { timeout: 15000 })
    if (!response.ok()) throw new Error(`test-room archive failed: ${response.status()}`)
  } finally {
    await control.context.dispose().catch(()=>{})
  }
}

// init script: override getUserMedia/getDisplayMedia with live canvas streams,
// record constraints, and count applyConstraints calls on the video track.
function initScript(label, portrait, rewriteMdnsCandidates) {
  return `(${(label, portrait, rewriteMdnsCandidates) => {
    const W = portrait ? 480 : 640, H = portrait ? 640 : 480
    window.__LABEL = label
    function makeStream(text) {
      const c = document.createElement('canvas'); c.width = W; c.height = H
      const x = c.getContext('2d'); let f = 0
      setInterval(() => { f++; x.fillStyle = 'hsl(' + ((f*4)%360) + ',70%,45%)'; x.fillRect(0,0,W,H)
        x.fillStyle='#fff'; x.font='32px sans-serif'; x.fillText(text + ' ' + f, 16, H/2) }, 33)
      return c.captureStream(30)
    }
    let camStream = null
    const cameraStream = () => {
      camStream ||= makeStream(label)
      return camStream
    }
    function audioTrack() {
      const ac = new (window.AudioContext||window.webkitAudioContext)(); ac.resume && ac.resume()
      const d = ac.createMediaStreamDestination(); const o = ac.createOscillator(); o.frequency.value = 110
      const g = ac.createGain(); g.gain.value = 0.001; o.connect(g); g.connect(d); o.start()
      return d.stream.getAudioTracks()[0]
    }
    const gumCalls = []
    let applyConstraintsCalls = 0
    Object.defineProperties(window, {
      __gumCalls: { configurable: false, get: () => gumCalls },
      __applyConstraintsCalls: { configurable: false, get: () => applyConstraintsCalls }
    })
    // Playwright's bundled WebKit intentionally exposes only an obfuscated
    // mDNS host candidate. On macOS CI Pion cannot resolve that synthetic
    // `.local` name (the browser and SFU still share the same host), leaving
    // the otherwise-valid offer/answer in ICE `checking` until the 30s
    // watchdog reconnects it. Translate only the harness's outbound WebKit
    // candidate address to loopback; media still traverses the real browser
    // ICE/DTLS/SRTP stack and real Go SFU.
    let mdnsCandidateRewrites = 0
    Object.defineProperty(window, '__mdnsCandidateRewrites', {
      configurable: false,
      get: () => mdnsCandidateRewrites
    })
    if (rewriteMdnsCandidates) {
      const send = WebSocket.prototype.send
      WebSocket.prototype.send = function(data) {
        if (typeof data === 'string') {
          try {
            const message = JSON.parse(data)
            if (message?.event === 'candidate' && typeof message.data === 'string') {
              const payload = JSON.parse(message.data)
              const candidate = String(payload?.candidate || '')
              const rewritten = candidate.replace(/(\s)([a-z0-9-]+\.local)(\s)/i, (_match, before, _host, after) => `${before}127.0.0.1${after}`)
              if (rewritten !== candidate) {
                payload.candidate = rewritten
                message.data = JSON.stringify(payload)
                data = JSON.stringify(message)
                mdnsCandidateRewrites += 1
              }
            }
          } catch (_) {
            // Preserve the exact application frame when it is not JSON.
          }
        }
        return send.call(this, data)
      }
    }
    const wrapTrack = t => {
      if (!t) return t
      const orig = t.applyConstraints && t.applyConstraints.bind(t)
      if (orig) t.applyConstraints = (...a) => { applyConstraintsCalls++; return orig(...a) }
      return t
    }
    const getUserMedia = async (constraints) => {
      gumCalls.push(JSON.parse(JSON.stringify(constraints||{})))
      const tracks = []
      if (constraints && constraints.video) tracks.push(wrapTrack(cameraStream().getVideoTracks()[0]))
      if (constraints && constraints.audio) tracks.push(audioTrack())
      return new MediaStream(tracks)
    }
    const enumerateDevices = async () => ([
      { deviceId:'cam', kind:'videoinput', label:'fake cam', groupId:'g' },
      { deviceId:'mic', kind:'audioinput', label:'fake mic', groupId:'g' },
    ])
    const getDisplayMedia = async () => {
      window.__sharedScreen = true
      return makeStream('SCREEN ' + label)
    }
    // The init script can run before WebKit exposes MediaDevices for the final
    // secure document. Keep an idempotent installer and call it again after
    // DOMContentLoaded; verify identity so a native permission prompt can never
    // masquerade as the synthetic-media harness.
    const installMediaOverrides = () => {
      const mediaDevices = navigator.mediaDevices
      if (!mediaDevices) return false
      const install = (name, value) => {
        try { mediaDevices[name] = value } catch (_) {}
        if (mediaDevices[name] !== value) {
          try { Object.defineProperty(mediaDevices, name, { configurable: true, value }) } catch (_) {}
        }
        return mediaDevices[name] === value
      }
      return install('getUserMedia', getUserMedia)
        && install('enumerateDevices', enumerateDevices)
        && install('getDisplayMedia', getDisplayMedia)
    }
    Object.defineProperty(window, '__installHarnessMediaOverrides', {
      configurable: false,
      value: installMediaOverrides
    })
    window.__harnessMediaOverridesInstalled = installMediaOverrides()
  }})(${JSON.stringify(label)}, ${JSON.stringify(portrait)}, ${JSON.stringify(rewriteMdnsCandidates)})`
}

async function join(browserType, ctxOpts, label, portrait) {
  const browser = await browserType.launch(
    browserType === chromium && CHROME_PATH ? { executablePath: CHROME_PATH } : undefined
  )
  let ready = false
  try {
  const context = await browser.newContext({ ...ctxOpts, permissions: ['camera','microphone'].filter(()=>browserType===chromium) })
  // Install one context-owned script before any page exists. This avoids the
  // undefined ordering/lifecycle edge of multiple page-level init scripts and
  // makes an empty request recorder an explicit harness failure.
  await context.addInitScript(initScript(label, portrait, browserType === webkit))
  // login -> cookie in context jar
  const resp = await context.request.post(BASE + '/auth/login', { data: { name: label, password: PW } })
  if (!resp.ok()) throw new Error(label+' login failed: '+resp.status())
  const page = await context.newPage()
  page.on('console', m => { const t = m.text(); if (/error|fail/i.test(t) && !/auth\/me|401/.test(t)) console.log(`   [${label} console] ${t.slice(0,120)}`) })
  page.on('pageerror', error => console.log(`   [${label} pageerror] ${error.stack || error.message}`))
  await page.goto(BASE, { waitUntil: 'domcontentloaded', timeout: 60000 })
  await page.waitForLoadState('domcontentloaded')
  const mediaOverridesInstalled = await page.evaluate(() => {
    const installed = window.__installHarnessMediaOverrides?.() === true
    window.__harnessMediaOverridesInstalled = installed
    return installed
  })
  if (!mediaOverridesInstalled) {
    throw new Error(`${label} synthetic media overrides were not installed on the final document`)
  }
  await page.waitForFunction(() => window.__bonfireClientBootReady === true, null, { timeout: 30000 })
  if (roomControl?.id) {
    await page.evaluate(async () => { await loadRoomsList() })
    await page.waitForFunction(roomId => (
      Array.isArray(roomsList)
      && roomsList.some(room => room?.id === roomId && !room.archived)
    ), roomControl.id, { timeout: 30000 })
  }
  await page.selectOption('#loginAccountSelect', label).catch(()=>{})
  await page.fill('#roomPassword', PW).catch(()=>{})
  await page.evaluate(async (roomId) => {
    // Navigate through the canonical product route. Calling setActiveTool from
    // `/` is intentionally ignored because Home owns that path; the old
    // harness therefore decoded off-screen video and called zero-sized tiles
    // a layout regression.
    selectPD1Destination('Video', { focus: false })
    if (roomId) selectLobbyRoom(roomId)
    await joinRoom()
  }, roomControl?.id || '')
  // joinRoom can return after opening signaling but before the access-granted
  // render has settled. Keep every later assertion tied to a participant that
  // is actually seated and publishing a live local camera, not merely a page
  // whose join call resolved. Do not require the hidden/local self-preview to
  // decode in headless WebKit; the call gate below requires a rendered REMOTE
  // participant before the flicker watch begins.
  try {
    await page.waitForFunction(() => {
      const stream = typeof localStream !== 'undefined' ? localStream : null
      return document.getElementById('appShell')?.classList.contains('is-in-room')
        && stream?.getVideoTracks?.().some(track => track.readyState === 'live')
        && Array.isArray(window.__gumCalls)
        && window.__gumCalls.some(call => call?.video)
    }, null, { timeout: JOIN_TIMEOUT_MS })
  } catch (error) {
    throw new Error(`${label} did not reach seated and publishing readiness within ${JOIN_TIMEOUT_MS}ms: ${error.message}`)
  }
  ready = true
  return { browser, context, page, label }
  } finally {
    if (!ready) {
      await withTimeout(browser.close(), 10000, `${label} failed-join browser cleanup timed out`).catch(()=>{})
    }
  }
}

async function mediaStateSnapshot(page) {
  return page.evaluate(() => {
    const summarizeTrack = track => track ? {
      id: track.id,
      kind: track.kind,
      readyState: track.readyState,
      enabled: track.enabled,
      muted: track.muted
    } : null
    const summarizeElement = element => ({
      tag: element.tagName.toLowerCase(),
      participant: element.dataset?.participant || '',
      connected: element.isConnected,
      paused: element.paused,
      muted: element.muted,
      defaultMuted: element.defaultMuted,
      volume: element.volume,
      currentTime: Number(element.currentTime || 0).toFixed(3),
      readyState: element.readyState,
      videoWidth: element.videoWidth || 0,
      videoHeight: element.videoHeight || 0,
      playback: element.dataset?.remotePlayback || '',
      audibleState: element.dataset?.remoteAudibleState || '',
      audibleGeneration: element.dataset?.remoteAudibleGeneration || '',
      attachmentRevision: element.dataset?.videoAttachmentRevision || '',
      audioTracks: (element.srcObject?.getAudioTracks?.() || []).map(summarizeTrack),
      videoTracks: (element.srcObject?.getVideoTracks?.() || []).map(summarizeTrack),
      rect: (() => {
        const rect = element.getBoundingClientRect()
        return { width: Math.round(rect.width), height: Math.round(rect.height) }
      })()
    })
    const videos = [...document.querySelectorAll('#videoStack video')].map(summarizeElement)
    const audioElements = [...document.querySelectorAll('audio')].map(summarizeElement)
    const monitors = typeof audioMonitors === 'undefined' ? [] : [...audioMonitors.entries()].map(([key, monitor]) => ({
      key,
      name: monitor?.name || '',
      track: summarizeTrack(monitor?.track),
      ownerTag: monitor?.audio?.tagName?.toLowerCase?.() || '',
      ownerParticipant: monitor?.audio?.dataset?.participant || '',
      ownerIsCanonicalVideo: Boolean(monitor?.audio && remoteVideoElementForParticipant(monitor.name) === monitor.audio),
      ownsAudioElement: Boolean(monitor?.ownsAudioElement),
      playbackGeneration: monitor?.playbackGeneration || 0
    }))
    const receipts = typeof remoteAudiblePlaybackReceipts === 'undefined' ? [] : [...remoteAudiblePlaybackReceipts.values()].map(receipt => ({
      name: receipt?.name || '',
      generation: receipt?.generation || 0,
      monitorGeneration: receipt?.monitorGeneration || 0,
      track: summarizeTrack(receipt?.track),
      current: remoteAudiblePlaybackReceiptIsCurrent(receipt),
      element: summarizeElement(receipt.element)
    }))
    return {
      peer: typeof pc === 'undefined' || !pc ? null : {
        signalingState: pc.signalingState,
        connectionState: pc.connectionState,
        iceConnectionState: pc.iceConnectionState,
        iceGatheringState: pc.iceGatheringState,
        remoteDescriptionType: pc.remoteDescription?.type || '',
        localDescriptionType: pc.localDescription?.type || ''
      },
      websocket: typeof ws === 'undefined' || !ws ? null : { readyState: ws.readyState, url: ws.url },
      reconnect: {
        active: typeof isSignalReconnecting !== 'undefined' && isSignalReconnecting,
        attempts: typeof signalingReconnectAttempts !== 'undefined' ? signalingReconnectAttempts : 0,
        reason: typeof lastSignalReconnectReason !== 'undefined' ? lastSignalReconnectReason : ''
      },
      pendingPlayback: typeof pendingRemotePlaybackElements === 'undefined' ? 0 : pendingRemotePlaybackElements.size,
      proof: window.__bonfireMediaSoakProbe?.snapshot?.() || null,
      videos,
      audioElements,
      monitors,
      receipts
    }
  })
}

async function canonicalRemoteAVSnapshot(page, participantName) {
  return page.evaluate(name => {
    const wanted = String(name || '').trim().toLowerCase()
    const video = [...document.querySelectorAll('#videoStack .video-tile video')]
      .find(element => String(element.closest('[data-participant]')?.dataset?.participant || '').trim().toLowerCase() === wanted)
    const monitorEntries = typeof audioMonitors === 'undefined' ? [] : [...audioMonitors.entries()]
      .filter(([, monitor]) => String(monitor?.name || '').trim().toLowerCase() === wanted)
    const monitor = monitorEntries[0]?.[1]
    const receipt = video && typeof remoteAudiblePlaybackReceipts !== 'undefined'
      ? remoteAudiblePlaybackReceipts.get(video)
      : null
    const participantMedia = [...document.querySelectorAll('video, audio')]
      .filter(element => String(element.dataset?.participant || element.closest?.('[data-participant]')?.dataset?.participant || '').trim().toLowerCase() === wanted)
    const audioBearing = participantMedia.filter(element => element.srcObject?.getAudioTracks?.().some(track => track.readyState === 'live'))
    const audible = audioBearing.filter(element => !element.muted && !element.defaultMuted && Number(element.volume) > 0)
    return {
      exists: Boolean(video),
      state: video?.dataset?.remoteAudibleState || '',
      muted: video?.muted,
      defaultMuted: video?.defaultMuted,
      volume: video?.volume,
      paused: video?.paused,
      currentTime: Number(video?.currentTime || 0),
      videoWidth: video?.videoWidth || 0,
      videoHeight: video?.videoHeight || 0,
      attachmentRevision: Number(video?.dataset?.videoAttachmentRevision || 0),
      audioTracks: video?.srcObject?.getAudioTracks?.().filter(track => track.readyState === 'live').length || 0,
      videoTracks: video?.srcObject?.getVideoTracks?.().filter(track => track.readyState === 'live').length || 0,
      monitorCount: monitorEntries.length,
      monitorOwnsVideo: Boolean(monitor && monitor.audio === video && !monitor.ownsAudioElement),
      receiptCurrent: Boolean(receipt && remoteAudiblePlaybackReceiptIsCurrent(receipt)),
      audioBearingElements: audioBearing.length,
      audibleElements: audible.length,
      separateAudioElements: participantMedia.filter(element => element.tagName === 'AUDIO').length,
      pending: typeof pendingRemotePlaybackElements === 'undefined' ? 0 : pendingRemotePlaybackElements.size
    }
  }, participantName)
}

const sessions = []
try {
  roomControl = await createIsolatedRoom()
  if (roomControl) console.log(`[setup] isolated room: ${ROOM_NAME}`)
  console.log('[setup] A=Chromium(AJ), B=WebKit/Safari(Tim) join the same room')
  const A = await join(chromium, {}, 'AJ', false)
  sessions.push(A)
  const B = await join(webkit, {}, 'Tim', false)
  sessions.push(B)

  console.log('\n[0] Room canvas — light follows paper; dark remains true black')
  const roomCanvasTheme = await A.page.evaluate(() => {
    const root = document.documentElement
    const initialTheme = root.dataset.theme || 'light'
    const snapshot = theme => {
      root.dataset.theme = theme
      const presentation = document.querySelector('.hearth-presentation')
      const stage = document.getElementById('hearthStage')
      const video = document.querySelector('#videoStack video')
      return {
        presentation: presentation ? getComputedStyle(presentation).backgroundColor : '',
        stage: stage ? getComputedStyle(stage).backgroundColor : '',
        video: video ? getComputedStyle(video).backgroundColor : ''
      }
    }
    const light = snapshot('light')
    const dark = snapshot('dark')
    root.dataset.theme = initialTheme
    return { light, dark }
  })
  console.log('   canvas colors:', JSON.stringify(roomCanvasTheme))
  ok('Light room canvas uses the warm paper ground while video stays black',
    roomCanvasTheme.light.presentation === 'rgb(207, 197, 183)'
      && roomCanvasTheme.light.stage === 'rgb(207, 197, 183)'
      && roomCanvasTheme.light.video === 'rgb(0, 0, 0)')
  ok('Dark room canvas and video remain true black',
    roomCanvasTheme.dark.presentation === 'rgb(0, 0, 0)'
      && roomCanvasTheme.dark.stage === 'rgb(0, 0, 0)'
      && roomCanvasTheme.dark.video === 'rgb(0, 0, 0)')

  // wait until B (Safari engine) is in a real call: a REMOTE participant tile
  // exists with a rendering remote video element (live track + dimensions).
  // Monitoring self-video could pass while the actual receiver path is black.
  const conn = await B.page.waitForFunction(selfName => {
    const own = String(selfName || '').trim().toLowerCase()
    const tiles = [...document.querySelectorAll('#videoStack .video-tile')]
      .filter(tile => String(tile.dataset.participant || '').trim())
    const remoteTiles = tiles.filter(tile => String(tile.dataset.participant || '').trim().toLowerCase() !== own)
    const remoteVideos = remoteTiles.flatMap(tile => [...tile.querySelectorAll('video')])
      .filter(video => video.srcObject
        && video.srcObject.getVideoTracks().some(track => track.readyState === 'live')
        && video.videoWidth > 0
        && video.videoHeight > 0)
    if (remoteTiles.length === 0 || remoteVideos.length === 0) return null
    const mon = remoteVideos[0]
    if (!mon.id) mon.id = 'pwRemoteMon'
    return {
      monId: mon.id,
      rendering: remoteVideos.length,
      tiles: tiles.map(tile => tile.dataset.participant),
      monitoredParticipant: mon.closest('[data-participant]')?.dataset.participant || ''
    }
  }, B.label, { timeout: JOIN_TIMEOUT_MS }).then(h=>h.jsonValue()).catch(()=>null)

  console.log('\n[1] Flicker — WebKit (Safari engine) in a REAL WebRTC call')
  console.log('   conn:', JSON.stringify(conn))
  if (!conn) {
    console.log('   WebKit media diagnostics:', JSON.stringify(await mediaStateSnapshot(B.page)))
  }
  ok('Safari-engine peer is in a real call (remote tile + live rendering video)', !!conn && conn.rendering>0)
  const webkitMdnsRewrites = await B.page.evaluate(() => window.__mdnsCandidateRewrites || 0)
  ok('Safari-engine harness crossed real ICE/DTLS/SRTP through its loopback mDNS adapter', webkitMdnsRewrites > 0)
  if (conn && conn.monId) {
    const mutedFirst = await canonicalRemoteAVSnapshot(B.page, conn.monitoredParticipant)
    await sleep(300)
    const mutedProgress = await canonicalRemoteAVSnapshot(B.page, conn.monitoredParticipant)
    console.log('   muted-first canonical A/V:', JSON.stringify(mutedProgress))
    ok('Safari-engine renders the exact combined owner muted before audio permission',
      mutedFirst.exists
        && mutedFirst.state === 'blocked'
        && mutedFirst.muted === true
        && mutedFirst.audioTracks === 1
        && mutedFirst.videoTracks === 1
        && mutedFirst.monitorCount === 1
        && mutedFirst.monitorOwnsVideo === true
        && mutedFirst.receiptCurrent === true
        && mutedProgress.currentTime > mutedFirst.currentTime)
    ok('Muted-first rendering has one canonical audio-bearing owner and no audio clone',
      mutedFirst.audioBearingElements === 1
        && mutedFirst.audibleElements === 0
        && mutedFirst.separateAudioElements === 0)

    // A real Playwright pointer input carries WebKit user activation. The
    // document-level unlock path must promote this exact receipt without
    // creating or waking a second audio owner, and video must keep advancing.
    await B.page.mouse.click(12, 12)
    const audible = await B.page.waitForFunction(name => {
      const wanted = String(name || '').trim().toLowerCase()
      const video = [...document.querySelectorAll('#videoStack .video-tile video')]
        .find(element => String(element.closest('[data-participant]')?.dataset?.participant || '').trim().toLowerCase() === wanted)
      return video?.dataset?.remoteAudibleState === 'audible'
        && video.muted === false
        && video.defaultMuted === false
        && Number(video.volume) === 1
        && video.videoWidth > 0
    }, conn.monitoredParticipant, { timeout: 5000 }).then(() => canonicalRemoteAVSnapshot(B.page, conn.monitoredParticipant)).catch(() => null)
    console.log('   after trusted gesture:', JSON.stringify(audible))
    ok('Trusted gesture promotes the exact current owner to audible without duplicating it',
      audible?.state === 'audible'
        && audible?.audioBearingElements === 1
        && audible?.audibleElements === 1
        && audible?.separateAudioElements === 0
        && audible?.monitorOwnsVideo === true
        && audible?.receiptCurrent === true
        && audible?.pending === 0)

    // Monitor beyond the server's 30s watchdog. Each flicker reattaches a new
    // MediaStream (force:true); a failed ICE/signaling session replaces both
    // WebSocket and PeerConnection. All three identities must remain stable.
    const churn = await B.page.evaluate(async (id) => {
      const v = document.getElementById(id)
      const tid = el => el.srcObject?.getVideoTracks?.()[0]?.id || ''
      const initialWs = ws, initialPc = pc
      let lastObj = v.srcObject, lastTid = tid(v)
      const initialObj = lastObj, initialTid = lastTid
      const initialAttachmentRevision = Number(v.dataset.videoAttachmentRevision || 0)
      const initialCurrentTime = Number(v.currentTime || 0)
      let totalSwaps = 0, flickerSwaps = 0, contentSwitches = 0
      const t0 = performance.now()
      while (performance.now() - t0 < 36000) {
        await new Promise(r=>setTimeout(r,150))
        if (v.srcObject !== lastObj) {
          totalSwaps++
          const now = tid(v)
          if (now && now === lastTid) flickerSwaps++   // same track, new MediaStream => the flicker reattach
          else contentSwitches++                        // different track => legit active-speaker switch
          lastObj = v.srcObject; lastTid = now
        }
      }
      return {
        totalSwaps,
        flickerSwaps,
        contentSwitches,
        playing: !v.paused && v.readyState>=2,
        vw: v.videoWidth,
        currentTimeAdvanced: Number(v.currentTime || 0) > initialCurrentTime,
        sameWebsocket: ws === initialWs && ws?.readyState === WebSocket.OPEN,
        samePeer: pc === initialPc && pc?.connectionState === 'connected',
        sameElementStream: v.srcObject === initialObj,
        sameTrack: tid(v) === initialTid,
        sameAttachmentRevision: Number(v.dataset.videoAttachmentRevision || 0) === initialAttachmentRevision
      }
    }, conn.monId)
    console.log('   playback element over 36s:', JSON.stringify(churn))
    ok('Safari-engine: ZERO same-track srcObject reattaches (the flicker is gone)', churn.flickerSwaps === 0)
    ok('Safari-engine playback element keeps rendering throughout', churn.playing === true && churn.vw>0 && churn.currentTimeAdvanced)
    ok('Safari-engine session and exact attachment stay stable beyond the 30s watchdog',
      churn.sameWebsocket && churn.samePeer && churn.sameElementStream && churn.sameTrack && churn.sameAttachmentRevision)
  }

  console.log('\n[2] Mobile orientation — C joins as a real iPhone (portrait), effect on others')
  // snapshot A & B local/remote orientation before C joins
  const orient = async (s) => s.page.evaluate(() => {
    const vids = [...document.querySelectorAll('#videoStack video')].filter(v=>v.videoWidth>0)
    return vids.map(v => v.videoWidth >= v.videoHeight ? 'landscape' : 'portrait')
  })
  const beforeA = await orient(A), beforeB = await orient(B)
  // The native app is libwebrtc, not Mobile Safari. Use Chromium's WebRTC
  // engine with the real iPhone viewport/UA so the publisher stays stable,
  // while B remains the Safari/WebKit receiver that must render the portrait
  // composition correctly.
  const C = await join(chromium, { ...devices['iPhone 13'] }, 'Erick', true)
  sessions.push(C)
  await sleep(8000) // let C negotiate + roster propagate + any retune fire
  const cInfo = await C.page.evaluate(() => {
    const track = typeof localStream !== 'undefined' ? localStream?.getVideoTracks?.()[0] : null
    return {
      gum: window.__gumCalls,
      applied: window.__applyConstraintsCalls,
      mobile: /Mobi|Android|iPhone|iPad|iPod/i.test(navigator.userAgent),
      localVideoLive: Boolean(track && track.readyState === 'live')
    }
  })
  const afterA = await orient(A), afterB = await orient(B)
  const remotePortraitFrame = async (session, participantName) => session.page.waitForFunction(name => {
    const target = String(name || '').trim().toLowerCase()
    const tile = [...document.querySelectorAll('#videoStack .video-tile')]
      .find(candidate => String(candidate.dataset.participant || '').trim().toLowerCase() === target)
    const video = tile?.querySelector('video')
    if (!video || !video.srcObject || video.videoWidth <= 0 || video.videoHeight <= 0) return null
    const rect = video.getBoundingClientRect()
    const tileRect = tile.getBoundingClientRect()
    return {
      videoWidth: video.videoWidth,
      videoHeight: video.videoHeight,
      visualWidth: Math.round(rect.width),
      visualHeight: Math.round(rect.height),
      tileWidth: Math.round(tileRect.width),
      tileHeight: Math.round(tileRect.height),
      frameOrientation: video.dataset.frameOrientation || 'unknown',
      objectFit: getComputedStyle(video).objectFit,
      position: getComputedStyle(video).position,
      portraitComposition: tile.classList.contains('has-portrait-composition'),
      backdropReady: tile.querySelector('.portrait-frame-backdrop.is-ready') !== null
    }
  }, participantName, { timeout: 25000 }).then(handle => handle.jsonValue()).catch(() => null)
  const cOnA = await remotePortraitFrame(A, C.label)
  const cOnB = await remotePortraitFrame(B, C.label)
  console.log('   C(iPhone) gum constraints:', JSON.stringify(cInfo.gum))
  console.log('   C applyConstraints calls:', cInfo.applied, '| A orient', beforeA,'->',afterA, '| B orient', beforeB,'->',afterB)
  console.log('   C frame on A(Chromium):', JSON.stringify(cOnA), '| B(WebKit):', JSON.stringify(cOnB))
  const cVideoConstraint = ((cInfo.gum || []).find(g=>g.video) || {}).video
  const land = a => a.filter(o=>o==='landscape').length
  ok('C detected as mobile (real iPhone UA)', cInfo.mobile === true)
  ok('C publishes a live local camera track', cInfo.localVideoLive === true)
  ok('C capture requested WITHOUT a landscape aspectRatio pin', cVideoConstraint
    && typeof cVideoConstraint === 'object'
    && !('aspectRatio' in cVideoConstraint))
  ok('C never restarted its camera via applyConstraints (no reorientation trigger)', cInfo.applied === 0)
  // The bug = existing landscape feeds FLIP to portrait when a mobile joins. So
  // the landscape count must not DROP (C's own feed is legitimately portrait).
  ok('A: existing landscape feeds did NOT flip to portrait when C joined', land(afterA) >= land(beforeA) && land(afterA) >= 1)
  ok('B: existing landscape feeds did NOT flip to portrait when C joined', land(afterB) >= land(beforeB) && land(afterB) >= 1)
  ok('A: portrait iPhone camera uses the full-frame ambient composition', cOnA?.videoHeight > cOnA?.videoWidth
    && cOnA?.frameOrientation === 'portrait'
    && cOnA?.objectFit === 'contain'
    && cOnA?.portraitComposition === true
    && cOnA?.backdropReady === true
    && cOnA?.position === 'absolute'
    && Math.abs(cOnA?.visualWidth - cOnA?.tileWidth) <= 1
    && Math.abs(cOnA?.visualHeight - cOnA?.tileHeight) <= 1)
  ok('B (Safari engine): portrait iPhone camera uses the full-frame ambient composition', cOnB?.videoHeight > cOnB?.videoWidth
    && cOnB?.frameOrientation === 'portrait'
    && cOnB?.objectFit === 'contain'
    && cOnB?.portraitComposition === true
    && cOnB?.backdropReady === true
    && cOnB?.position === 'absolute'
    && Math.abs(cOnB?.visualWidth - cOnB?.tileWidth) <= 1
    && Math.abs(cOnB?.visualHeight - cOnB?.tileHeight) <= 1)

  console.log('\n[3] Screen share — A shares; B and C must see it on the presentation stage')
  await A.page.click('#screenShare').catch(async()=>{ await A.page.evaluate(()=>document.getElementById('screenShare')?.click()) })
  const stageVisible = async (s) => s.page.waitForFunction(() => {
    const tile = document.getElementById('presentationTile')
    const v = document.getElementById('screenStageVideo')
    if (!(tile && tile.classList.contains('is-screen-sharing') && v && v.srcObject &&
      v.srcObject.getVideoTracks().some(t=>t.readyState==='live') && v.videoWidth > 0 && v.videoHeight > 0)) return false
    try {
      const canvas = document.createElement('canvas')
      canvas.width = 64
      canvas.height = 36
      const context = canvas.getContext('2d', { willReadFrequently: true })
      context.drawImage(v, 0, 0, canvas.width, canvas.height)
      const data = context.getImageData(0, 0, canvas.width, canvas.height).data
      let nonBlackPixels = 0
      let maxLuma = 0
      for (let offset = 0; offset < data.length; offset += 4) {
        if (Math.max(data[offset], data[offset + 1], data[offset + 2]) >= 12) nonBlackPixels += 1
        maxLuma = Math.max(maxLuma, ((0.2126 * data[offset]) + (0.7152 * data[offset + 1]) + (0.0722 * data[offset + 2])) / 255)
      }
      return nonBlackPixels / Math.max(1, data.length / 4) >= 0.05 && maxLuma >= 0.08
    } catch {
      return false
    }
  }, null, { timeout: 25000 }).then(()=>true).catch(()=>false)
  const bSees = await stageVisible(B)
  const cSees = await stageVisible(C)
  const stageFit = await B.page.evaluate(() => {
    const video = document.getElementById('screenStageVideo')
    if (!video) return null
    const style = getComputedStyle(video)
    return { objectFit: style.objectFit, objectPosition: style.objectPosition }
  })
  const stageViewport = await B.page.evaluate(() => {
    const scroller = document.scrollingElement
    const presentation = document.getElementById('presentationTile')
    const stage = document.getElementById('screenStage')
    const video = document.getElementById('screenStageVideo')
    if (!(scroller && presentation && stage && video)) return null
    const presentationRect = presentation.getBoundingClientRect()
    const stageRect = stage.getBoundingClientRect()
    const videoRect = video.getBoundingClientRect()
    return {
      clientHeight: scroller.clientHeight,
      scrollHeight: scroller.scrollHeight,
      presentationBottom: Math.round(presentationRect.bottom),
      stageBottom: Math.round(stageRect.bottom),
      videoBottom: Math.round(videoRect.bottom)
    }
  })
  console.log('   B sees screen stage:', bSees, '| C sees screen stage:', cSees)
  console.log('   B screen-stage viewport:', JSON.stringify(stageViewport))
  ok('B (Safari engine) sees the shared screen on the presentation stage', bSees)
  ok('C (iPhone) sees the shared screen on the presentation stage', cSees)
  ok('Shared content is fully visible and centered', stageFit?.objectFit === 'contain'
    && stageFit?.objectPosition === '50% 50%')
  ok('Desktop shared-screen stage stays inside one viewport without document scroll',
    stageViewport?.scrollHeight <= stageViewport?.clientHeight + 2
      && stageViewport?.presentationBottom <= stageViewport?.clientHeight + 2
      && stageViewport?.stageBottom <= stageViewport?.presentationBottom + 2
      && stageViewport?.videoBottom <= stageViewport?.stageBottom + 2)

  console.log('\n[4] Screen share SURVIVES renegotiation — D joins WHILE A is sharing')
  // A new participant joining triggers SDP renegotiation on every peer. The old
  // code rebound outbound video to the CAMERA track on renegotiation, killing the
  // share for everyone. outboundTrackForKind() must keep the screen track bound.
  const D = await join(chromium, {}, 'Tyler', false)
  sessions.push(D)
  await sleep(8000)
  const dSees = await stageVisible(D)
  const bStill = await stageVisible(B)
  const cStill = await stageVisible(C)
  console.log('   after D joined → D sees:', dSees, '| B still:', bStill, '| C still:', cStill)
  ok('D (late joiner) sees the shared screen', dSees)
  ok('B still sees the screen after renegotiation (share did NOT revert to camera)', bStill)
  ok('C still sees the screen after renegotiation', cStill)

} catch (e) {
  console.log('\nHARNESS ERROR:', e.stack || e.message)
  fail++
} finally {
  for (const s of sessions) {
    try {
      await withTimeout(s.browser.close(), 10000, `${s.label} browser cleanup timed out`)
    } catch (error) {
      console.log('\nHARNESS CLEANUP ERROR:', error.message)
      fail++
    }
  }
  try {
    await archiveIsolatedRoom(roomControl)
  } catch (error) {
    console.log('\nHARNESS CLEANUP ERROR:', error.message)
    fail++
  }
}

console.log(`\n==== ${pass} passed, ${fail} failed ====`)
process.exit(fail ? 1 : 0)
