// END-TO-END multi-party WebRTC verification of the 2026-06-16 media fixes,
// through the REAL Go SFU, in REAL browser engines (Chromium + WebKit/Safari),
// with getUserMedia/getDisplayMedia overridden to live canvas.captureStream()
// MediaStreams (no physical camera/phone/screen needed, but the real WebRTC +
// SFU + render paths all run). Reproduces what unit tests can't:
//   [1] a real WebRTC remote track rendered in the Safari engine shows ZERO
//       same-track srcObject reattaches over 18s (the flicker is gone);
//   [2] a real iPhone-13 WebKit peer joins portrait → detected mobile, capture
//       has no landscape aspectRatio pin, zero applyConstraints (no camera
//       restart), and existing landscape feeds do NOT flip to portrait;
//   [3] a shared screen reaches BOTH a Safari-engine peer and an iPhone peer on
//       the presentation stage.
//
// Run (needs Playwright + browsers; NOT in `go test`):
//   # 1) start an isolated local instance (separate data dir):
//   go build -o /tmp/ma . && rm -rf /tmp/ma-e2e && mkdir -p /tmp/ma-e2e/data
//   MEETING_ROOM_PASSWORD="smoke-pass-1234" \
//     MEETING_MEMORY_PATH=/tmp/ma-e2e/data/meeting-memory.jsonl /tmp/ma -addr :3191 &
//   # 2) install playwright + browsers, then run:
//   mkdir -p /tmp/e2e && cd /tmp/e2e && npm init -y && npm i playwright
//   npx playwright install webkit chromium
//   PLAYWRIGHT_BROWSERS_PATH="$HOME/Library/Caches/ms-playwright" \
//     node /Users/ajhart/meetingassist/scripts/media-fix-e2e-call.mjs
// Seeded accounts (accounts.go) all share MEETING_ROOM_PASSWORD locally.
import { webkit, chromium, devices } from 'playwright'

const BASE = 'http://localhost:3191'
const PW = 'smoke-pass-1234'
let pass = 0, fail = 0
const ok = (n,c)=>{ if(c){pass++;console.log('  PASS',n)}else{fail++;console.log('  FAIL',n)} }
const sleep = ms => new Promise(r=>setTimeout(r,ms))

// init script: override getUserMedia/getDisplayMedia with live canvas streams,
// record constraints, and count applyConstraints calls on the video track.
function initScript(label, portrait) {
  return `(${(label, portrait) => {
    const W = portrait ? 480 : 640, H = portrait ? 640 : 480
    function makeStream(text) {
      const c = document.createElement('canvas'); c.width = W; c.height = H
      const x = c.getContext('2d'); let f = 0
      setInterval(() => { f++; x.fillStyle = 'hsl(' + ((f*4)%360) + ',70%,45%)'; x.fillRect(0,0,W,H)
        x.fillStyle='#fff'; x.font='32px sans-serif'; x.fillText(text + ' ' + f, 16, H/2) }, 33)
      return c.captureStream(30)
    }
    const camStream = makeStream(window.__LABEL)
    function audioTrack() {
      const ac = new (window.AudioContext||window.webkitAudioContext)(); ac.resume && ac.resume()
      const d = ac.createMediaStreamDestination(); const o = ac.createOscillator(); o.frequency.value = 110
      const g = ac.createGain(); g.gain.value = 0.001; o.connect(g); g.connect(d); o.start()
      return d.stream.getAudioTracks()[0]
    }
    window.__gumCalls = []
    window.__applyConstraintsCalls = 0
    const wrapTrack = t => {
      if (!t) return t
      const orig = t.applyConstraints && t.applyConstraints.bind(t)
      if (orig) t.applyConstraints = (...a) => { window.__applyConstraintsCalls++; return orig(...a) }
      return t
    }
    navigator.mediaDevices.getUserMedia = async (constraints) => {
      window.__gumCalls.push(JSON.parse(JSON.stringify(constraints||{})))
      const tracks = []
      if (constraints && constraints.video) tracks.push(wrapTrack(camStream.getVideoTracks()[0]))
      if (constraints && constraints.audio) tracks.push(audioTrack())
      return new MediaStream(tracks)
    }
    navigator.mediaDevices.enumerateDevices = async () => ([
      { deviceId:'cam', kind:'videoinput', label:'fake cam', groupId:'g' },
      { deviceId:'mic', kind:'audioinput', label:'fake mic', groupId:'g' },
    ])
    navigator.mediaDevices.getDisplayMedia = async () => {
      window.__sharedScreen = true
      return makeStream('SCREEN ' + window.__LABEL)
    }
  }})(${JSON.stringify(label)}, ${JSON.stringify(portrait)})`
}

async function join(browserType, ctxOpts, label, portrait) {
  const browser = await browserType.launch()
  const context = await browser.newContext({ ...ctxOpts, permissions: ['camera','microphone'].filter(()=>browserType===chromium) })
  // login -> cookie in context jar
  const resp = await context.request.post(BASE + '/auth/login', { data: { name: label, password: PW } })
  if (!resp.ok()) throw new Error(label+' login failed: '+resp.status())
  const page = await context.newPage()
  page.on('console', m => { const t = m.text(); if (/error|fail/i.test(t) && !/auth\/me|401/.test(t)) console.log(`   [${label} console] ${t.slice(0,120)}`) })
  await page.addInitScript(`window.__LABEL = ${JSON.stringify(label)};`)
  await page.addInitScript(initScript(label, portrait))
  await page.goto(BASE, { waitUntil: 'domcontentloaded', timeout: 60000 })
  await page.waitForLoadState('domcontentloaded')
  await page.waitForFunction(() => typeof joinRoom === 'function' && typeof setActiveTool === 'function', null, { timeout: 30000 })
  await page.selectOption('#loginAccountSelect', label).catch(()=>{})
  await page.fill('#roomPassword', PW).catch(()=>{})
  await page.evaluate(async () => {
    setActiveTool('room')
    await joinRoom()
  })
  return { browser, context, page, label }
}

const sessions = []
try {
  console.log('[setup] A=Chromium(AJ), B=WebKit/Safari(Tim) join the same room')
  const A = await join(chromium, {}, 'AJ', false)
  const B = await join(webkit, {}, 'Tim', false)
  sessions.push(A, B)

  // wait until B (Safari engine) is in a real call: a remote participant tile
  // exists AND at least one rendering video element (live track + frames). Tag a
  // monitor element (prefer the consolidated active-speaker stage).
  const conn = await B.page.waitForFunction(() => {
    const tiles = [...document.querySelectorAll('#videoStack .video-tile')].map(t=>t.dataset.participant).filter(Boolean)
    const hasRemoteTile = tiles.some(n => n && n.toLowerCase() !== 'tim')
    const vids = [...document.querySelectorAll('video')].filter(v => v.srcObject &&
      v.srcObject.getVideoTracks().some(t=>t.readyState==='live') && v.videoWidth>0)
    if (!hasRemoteTile || vids.length === 0) return null
    const asv = document.getElementById('activeSpeakerVideo')
    const mon = (asv && asv.videoWidth>0 && asv.srcObject) ? asv : vids[0]
    if (mon && !mon.id) mon.id = 'pwMon'
    return { monId: mon ? (mon.id||'activeSpeakerVideo') : null, rendering: vids.length, tiles }
  }, null, { timeout: 35000 }).then(h=>h.jsonValue()).catch(()=>null)

  console.log('\n[1] Flicker — WebKit (Safari engine) in a REAL WebRTC call')
  console.log('   conn:', JSON.stringify(conn))
  ok('Safari-engine peer is in a real call (remote tile + live rendering video)', !!conn && conn.rendering>0)
  if (conn && conn.monId) {
    // monitor the playback element's srcObject for 15s — each flicker reattaches a
    // new MediaStream (setVideoElementStream force:true). This is the exact path
    // the fix guards, now on a real WebRTC stream in the real Safari engine.
    const churn = await B.page.evaluate(async (id) => {
      const v = document.getElementById(id)
      const tid = el => el.srcObject?.getVideoTracks?.()[0]?.id || ''
      let lastObj = v.srcObject, lastTid = tid(v)
      let totalSwaps = 0, flickerSwaps = 0, contentSwitches = 0
      const t0 = performance.now()
      while (performance.now() - t0 < 18000) {
        await new Promise(r=>setTimeout(r,150))
        if (v.srcObject !== lastObj) {
          totalSwaps++
          const now = tid(v)
          if (now && now === lastTid) flickerSwaps++   // same track, new MediaStream => the flicker reattach
          else contentSwitches++                        // different track => legit active-speaker switch
          lastObj = v.srcObject; lastTid = now
        }
      }
      return { totalSwaps, flickerSwaps, contentSwitches, playing: !v.paused && v.readyState>=2, vw: v.videoWidth }
    }, conn.monId)
    console.log('   playback element over 18s:', JSON.stringify(churn))
    ok('Safari-engine: ZERO same-track srcObject reattaches (the flicker is gone)', churn.flickerSwaps === 0)
    ok('Safari-engine playback element keeps rendering throughout', churn.playing === true && churn.vw>0)
  }

  console.log('\n[2] Mobile orientation — C joins as a real iPhone (portrait), effect on others')
  // snapshot A & B local/remote orientation before C joins
  const orient = async (s) => s.page.evaluate(() => {
    const vids = [...document.querySelectorAll('#videoStack video')].filter(v=>v.videoWidth>0)
    return vids.map(v => v.videoWidth >= v.videoHeight ? 'landscape' : 'portrait')
  })
  const beforeA = await orient(A), beforeB = await orient(B)
  const C = await join(webkit, { ...devices['iPhone 13'] }, 'Erick', true)
  sessions.push(C)
  await sleep(8000) // let C negotiate + roster propagate + any retune fire
  const cInfo = await C.page.evaluate(() => ({ gum: window.__gumCalls, applied: window.__applyConstraintsCalls,
    mobile: /Mobi|Android|iPhone|iPad|iPod/i.test(navigator.userAgent) }))
  const afterA = await orient(A), afterB = await orient(B)
  console.log('   C(iPhone) gum constraints:', JSON.stringify(cInfo.gum))
  console.log('   C applyConstraints calls:', cInfo.applied, '| A orient', beforeA,'->',afterA, '| B orient', beforeB,'->',afterB)
  const cVideoConstraint = (cInfo.gum.find(g=>g.video) || {}).video
  const land = a => a.filter(o=>o==='landscape').length
  ok('C detected as mobile (real iPhone UA)', cInfo.mobile === true)
  ok('C capture requested WITHOUT a landscape aspectRatio pin', cVideoConstraint && typeof cVideoConstraint==='object' && !('aspectRatio' in cVideoConstraint))
  ok('C never restarted its camera via applyConstraints (no reorientation trigger)', cInfo.applied === 0)
  // The bug = existing landscape feeds FLIP to portrait when a mobile joins. So
  // the landscape count must not DROP (C's own feed is legitimately portrait).
  ok('A: existing landscape feeds did NOT flip to portrait when C joined', land(afterA) >= land(beforeA) && land(afterA) >= 1)
  ok('B: existing landscape feeds did NOT flip to portrait when C joined', land(afterB) >= land(beforeB) && land(afterB) >= 1)

  console.log('\n[3] Screen share — A shares; B and C must see it on the presentation stage')
  await A.page.click('#screenShare').catch(async()=>{ await A.page.evaluate(()=>document.getElementById('screenShare')?.click()) })
  const stageVisible = async (s) => s.page.waitForFunction(() => {
    const tile = document.getElementById('presentationTile')
    const v = document.getElementById('screenStageVideo')
    return tile && tile.classList.contains('is-screen-sharing') && v && v.srcObject &&
      v.srcObject.getVideoTracks().some(t=>t.readyState==='live') && v.videoWidth > 0
  }, null, { timeout: 25000 }).then(()=>true).catch(()=>false)
  const bSees = await stageVisible(B)
  const cSees = await stageVisible(C)
  console.log('   B sees screen stage:', bSees, '| C sees screen stage:', cSees)
  ok('B (Safari engine) sees the shared screen on the presentation stage', bSees)
  ok('C (iPhone) sees the shared screen on the presentation stage', cSees)

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
  console.log('\nHARNESS ERROR:', e.message)
  fail++
} finally {
  for (const s of sessions) { await s.browser.close().catch(()=>{}) }
}

console.log(`\n==== ${pass} passed, ${fail} failed ====`)
process.exit(fail ? 1 : 0)
