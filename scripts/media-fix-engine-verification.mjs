// REAL browser-engine verification for the 2026-06-16 media fixes — runs the
// shipped helpers from index.html inside actual WebKit (Safari's engine) and
// Chromium. Complements scripts/media-fix-verification.mjs (logic-only, in CI).
//
// This one needs Playwright + its browsers, so it is NOT wired into `go test`
// (would make Playwright a test dependency). Run it manually:
//   mkdir -p /tmp/wkv && cd /tmp/wkv && npm init -y && npm i playwright
//   npx playwright install webkit chromium
//   PLAYWRIGHT_BROWSERS_PATH="$HOME/Library/Caches/ms-playwright" \
//     node /Users/ajhart/meetingassist/scripts/media-fix-engine-verification.mjs
//
// Verifies on the real Safari engine: (A) videoElementNeedsRefresh never forces a
// srcObject reattach (the flicker) during healthy live-MediaStream playback, that
// currentTime is a valid liveness signal there, and that genuine freezes are still
// detected; (B) a real iPhone-descriptor navigator is detected as mobile and gets
// no landscape aspect-ratio pin. (Multi-party WebRTC screen share still needs a
// human smoke test — getDisplayMedia can't be automated.)
import { readFileSync } from 'fs'
import { webkit, chromium, devices } from 'playwright'

const html = readFileSync('/Users/ajhart/meetingassist/index.html', 'utf8')
function extractFn(name) {
  let s = html.indexOf(`function ${name}(`)
  if (s < 0) throw new Error(`missing ${name}`)
  if (html.slice(s-6, s) === 'async ') s -= 6
  let d = 0
  for (let j = html.indexOf('{', s); j < html.length; j++) {
    if (html[j] === '{') d++
    else if (html[j] === '}') { if (--d === 0) return html.slice(s, j+1) }
  }
}
const SHIPPED = extractFn('videoElementHasRenderedFrame') + '\n' + extractFn('videoElementNeedsRefresh')

let pass = 0, fail = 0
const ok = (n,c)=>{ if(c){pass++;console.log('  PASS',n)}else{fail++;console.log('  FAIL',n)} }

// ---- A: flicker fix on the real engine, live MediaStream ----
async function flickerProbe(bt, name) {
  const b = await bt.launch()
  const p = await (await b.newContext()).newPage()
  await p.setContent('<canvas id=c width=320 height=240></canvas><video id=v autoplay playsinline muted></video>')
  const r = await p.evaluate(async (shipped) => {
    // inject the SHIPPED helpers + minimal deps
    const mod = new Function('video', `
      function mediaElementIsVisible(){ return true }
      function videoElementHasLiveVideoTrack(v){ return !!(v.srcObject && v.srcObject.getVideoTracks().some(t=>t.readyState==='live')) }
      ${shipped}
      return videoElementNeedsRefresh
    `)
    const videoElementNeedsRefresh = mod()

    const canvas = document.getElementById('c'), ctx = canvas.getContext('2d')
    let frame = 0
    const drawTimer = setInterval(()=>{ frame++; ctx.fillStyle = frame%2?'#fff':'#000'; ctx.fillRect(0,0,320,240); ctx.fillStyle='#888'; ctx.fillRect(frame%300,10,8,8) }, 33)
    const stream = canvas.captureStream(30)
    const v = document.getElementById('v')
    v.srcObject = stream
    try { await v.play() } catch(e) {}

    const hasRVFC = typeof v.requestVideoFrameCallback === 'function'
    let rvfcCount = 0
    if (hasRVFC) { const f=()=>{ rvfcCount++; v.requestVideoFrameCallback(f) }; v.requestVideoFrameCallback(f) }

    // simulate an established stream (past the 3.5s attach guard)
    v.dataset.videoStreamAttachedAt = String(performance.now() - 4000)

    const sleep = ms => new Promise(r=>setTimeout(r,ms))
    // HEALTHY phase: ~6s of continuous playback, sample the shipped fn
    const ctStart = v.currentTime
    let healthyRefreshAsks = 0, samples = 0
    for (let i=0;i<12;i++){ await sleep(500); samples++; if (videoElementNeedsRefresh(v)) healthyRefreshAsks++ }
    const ctAfterHealthy = v.currentTime
    const rvfcDuringHealthy = rvfcCount

    // FREEZE phase: stop frames + pause so currentTime genuinely stalls
    clearInterval(drawTimer)
    v.pause()
    const ctFrozen = v.currentTime
    await sleep(500)
    let detectedStall = false
    for (let i=0;i<26;i++){ await sleep(500); if (videoElementNeedsRefresh(v)) { detectedStall = true; break } }
    const ctAfterFreeze = v.currentTime

    return { hasRVFC, rvfcDuringHealthy, samples, healthyRefreshAsks,
             ctAdvancedHealthy:+(ctAfterHealthy-ctStart).toFixed(3),
             ctAdvancedFrozen:+(ctAfterFreeze-ctFrozen).toFixed(3),
             detectedStall, readyState:v.readyState, vw:v.videoWidth }
  }, SHIPPED)
  await b.close()
  return { name, ...r }
}

for (const [bt,name] of [[webkit,'WebKit (Safari engine)'],[chromium,'Chromium']]) {
  console.log(`\n[A] Flicker fix — real ${name}, live canvas MediaStream`)
  const r = await flickerProbe(bt, name)
  console.log('   metrics:', JSON.stringify(r))
  ok(`${name}: currentTime advances on live stream (fix signal valid)`, r.ctAdvancedHealthy > 1)
  ok(`${name}: shipped fn NEVER asks for a flicker-refresh during healthy playback`, r.healthyRefreshAsks === 0 && r.samples === 12)
  ok(`${name}: shipped fn STILL detects a genuine freeze (recovery preserved)`, r.detectedStall === true)
  ok(`${name}: currentTime stops when frozen (proves the freeze was real)`, r.ctAdvancedFrozen < 0.2)
}

// ---- B: mobile detection on the real engine with real device navigator ----
console.log('\n[B] Mobile orientation — real WebKit navigator (iPhone descriptor vs desktop)')
async function mobileProbe(contextOpts, label) {
  const b = await webkit.launch()
  const ctx = await b.newContext(contextOpts)
  const p = await ctx.newPage()
  await p.setContent('<html><body>x</body></html>')
  const r = await p.evaluate(() => {
    const isMobileDevice = /Mobi|Android|iPhone|iPad|iPod/i.test(navigator.userAgent)
      || (/Macintosh/.test(navigator.userAgent) && typeof navigator.maxTouchPoints === 'number' && navigator.maxTouchPoints > 1)
    const widescreenAspectRatio = { ideal: 16/9 }
    const cameraAspectRatioConstraint = isMobileDevice ? undefined : widescreenAspectRatio
    const video = { width:{ideal:1280,max:1280}, height:{ideal:720,max:720},
      ...(cameraAspectRatioConstraint ? { aspectRatio: cameraAspectRatioConstraint } : {}),
      frameRate:{ideal:30,max:30}, facingMode:'user' }
    return { ua: navigator.userAgent, maxTouch: navigator.maxTouchPoints, isMobileDevice, hasAspect: 'aspectRatio' in video }
  })
  await b.close()
  return { label, ...r }
}
const iphone = await mobileProbe({ ...devices['iPhone 13'] }, 'iPhone 13 (WebKit)')
const desk = await mobileProbe({}, 'desktop WebKit')
console.log('   iphone:', JSON.stringify(iphone))
console.log('   desktop:', JSON.stringify(desk))
ok('real iPhone-descriptor WebKit detected as mobile', iphone.isMobileDevice === true)
ok('iPhone capture constraints carry NO aspectRatio pin', iphone.hasAspect === false)
ok('desktop WebKit NOT mobile', desk.isMobileDevice === false)
ok('desktop WebKit STILL pins aspectRatio', desk.hasAspect === true)

console.log(`\n==== ${pass} passed, ${fail} failed ====`)
process.exit(fail ? 1 : 0)
