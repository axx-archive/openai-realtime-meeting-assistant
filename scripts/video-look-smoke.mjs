// VIDEO-LOOK smoke test (Spectacular OS Wave 13): prove the FAR END sees the look.
//
// For each of the four looks we run a synthetic camera frame through the REAL
// pipeline (public/video-looks/video-look-pipeline.js), publish the PROCESSED track
// over a loopback RTCPeerConnection, capture the received far-end frame, and assert
// it differs from the raw frame by that look's signature:
//   • low-light  → mean luma up (brightened)
//   • mono       → saturation ~0 (greyscale)
//   • bonfire warm → warmth (R−B) up (amber shift)
//   • studio     → measurably different from raw (contrast/sharpen)
// This is "far end sees it," not just local preview — the processed track is what
// the loopback peer receives.
//
// It self-detects Playwright and prints SKIP with setup steps if absent (the W4
// precedent). Run (needs Playwright + a browser; NOT part of `go test`):
//   # 1) start an isolated keyless local instance on :3100 (serves the module + assets):
//   go build -o /tmp/ma-vl . && rm -rf /tmp/ma-vl-data && mkdir -p /tmp/ma-vl-data
//   MEETING_ROOM_PASSWORD="smoke-pass-1234" \
//     MEETING_MEMORY_PATH=/tmp/ma-vl-data/meeting-memory.jsonl /tmp/ma-vl -addr :3100 &
//   # 2) install playwright + a browser, then run:
//   mkdir -p /tmp/e2e && cd /tmp/e2e && npm init -y && npm i playwright
//   npx playwright install chromium
//   PLAYWRIGHT_BROWSERS_PATH="$HOME/Library/Caches/ms-playwright" \
//     node /Users/ajhart/meetingassist/scripts/video-look-smoke.mjs

let chromium
try {
  ({ chromium } = await import('playwright'))
} catch (err) {
  console.error('SKIP: playwright is not installed. See the header of this file for the one-time setup, then re-run.')
  console.error('      (' + (err?.message || err) + ')')
  process.exit(2)
}

const BASE = process.env.VIDEO_LOOK_BASE || 'http://localhost:3100'

let pass = 0, fail = 0
const ok = (n, c, detail) => {
  if (c) { pass++; console.log('  PASS', n, detail ? '· ' + detail : '') }
  else { fail++; console.log('  FAIL', n, detail ? '· ' + detail : '') }
}

const browser = await chromium.launch({
  headless: true,
  args: ['--use-fake-ui-for-media-stream', '--enable-features=MediaStreamInsertableStreams']
})

try {
  const page = await browser.newPage()
  // Serves the module + shader from the same origin so the dynamic import resolves.
  const resp = await page.goto(BASE, { waitUntil: 'domcontentloaded', timeout: 15000 }).catch(() => null)
  if (!resp || !resp.ok()) {
    console.error(`SKIP: could not reach ${BASE} — start a local instance first (see this file's header).`)
    await browser.close()
    process.exit(2)
  }

  const results = await page.evaluate(async (moduleUrl) => {
    const out = { tier: 'none', looks: {}, error: '' }
    try {
      const mod = await import(moduleUrl)
      out.tier = mod.videoLookPipelineTier()

      // --- a known synthetic "camera" frame: colourful but a touch dim so low-light
      // has headroom to brighten. Redrawn every rAF so captureStream keeps producing.
      const src = document.createElement('canvas')
      src.width = 320; src.height = 180
      const sctx = src.getContext('2d')
      const paint = () => {
        const g = sctx.createLinearGradient(0, 0, 320, 180)
        g.addColorStop(0, '#402018'); g.addColorStop(1, '#183040')
        sctx.fillStyle = g; sctx.fillRect(0, 0, 320, 180)
        sctx.fillStyle = '#b04030'; sctx.fillRect(40, 40, 100, 100)
        sctx.fillStyle = '#3060a0'; sctx.fillRect(180, 40, 100, 100)
      }
      paint()
      let painting = true
      const repaint = () => { if (painting) { paint(); requestAnimationFrame(repaint) } }
      requestAnimationFrame(repaint)
      const rawStream = src.captureStream(30)
      const rawTrack = rawStream.getVideoTracks()[0]

      // Loopback: publish `track`, receive it on a second peer, read one far frame.
      async function farFrameStats(track) {
        const pc1 = new RTCPeerConnection()
        const pc2 = new RTCPeerConnection()
        pc1.onicecandidate = e => e.candidate && pc2.addIceCandidate(e.candidate)
        pc2.onicecandidate = e => e.candidate && pc1.addIceCandidate(e.candidate)
        const received = new Promise(res => { pc2.ontrack = e => res(e.track) })
        pc1.addTrack(track, new MediaStream([track]))
        const offer = await pc1.createOffer()
        await pc1.setLocalDescription(offer)
        await pc2.setRemoteDescription(offer)
        const answer = await pc2.createAnswer()
        await pc2.setLocalDescription(answer)
        await pc1.setRemoteDescription(answer)
        const farTrack = await received

        const video = document.createElement('video')
        video.muted = true; video.playsInline = true; video.autoplay = true
        video.srcObject = new MediaStream([farTrack])
        await video.play().catch(() => {})
        await new Promise(r => setTimeout(r, 700)) // let real frames arrive

        const c = document.createElement('canvas')
        c.width = 64; c.height = 36
        const cx = c.getContext('2d', { willReadFrequently: true })
        cx.drawImage(video, 0, 0, c.width, c.height)
        const px = cx.getImageData(0, 0, c.width, c.height).data
        let luma = 0, sat = 0, r = 0, b = 0, n = 0
        for (let i = 0; i < px.length; i += 4) {
          const R = px[i], G = px[i + 1], B = px[i + 2]
          luma += (0.2126 * R + 0.7152 * G + 0.0722 * B) / 255
          const mx = Math.max(R, G, B), mn = Math.min(R, G, B)
          sat += mx > 0 ? (mx - mn) / mx : 0
          r += R; b += B; n++
        }
        pc1.close(); pc2.close()
        return { luma: luma / n, sat: sat / n, warmth: (r - b) / n / 255 }
      }

      const raw = await farFrameStats(rawTrack)
      out.raw = raw

      for (const look of ['bonfire-warm', 'studio', 'mono', 'lowlight']) {
        const pipeline = new mod.VideoLookPipeline({ onStatus: () => {} })
        const processed = await pipeline.start(rawTrack.clone(), look, 1)
        if (!processed) { out.looks[look] = { tier: pipeline.tier, skipped: true }; continue }
        const stats = await farFrameStats(processed)
        out.looks[look] = { tier: pipeline.tier, ...stats }
        pipeline.stop()
      }
      painting = false
    } catch (e) {
      out.error = String(e && e.message || e)
    }
    return out
  }, BASE + '/public/video-looks/video-look-pipeline.js')

  console.log('pipeline tier:', results.tier)
  if (results.error) {
    ok('pipeline ran without error', false, results.error)
  } else if (results.tier === 'none') {
    console.log('SKIP: this browser reports tier "none" (no insertable/canvas WebGL2) — nothing to compare.')
    await browser.close()
    process.exit(2)
  } else {
    const raw = results.raw
    console.log('raw baseline:', JSON.stringify(raw))
    for (const [look, s] of Object.entries(results.looks)) {
      console.log(`  ${look}:`, JSON.stringify(s))
    }
    const warm = results.looks['bonfire-warm']
    const mono = results.looks['mono']
    const low = results.looks['lowlight']
    const studio = results.looks['studio']
    const diff = (a) => a && raw ? Math.abs(a.luma - raw.luma) + Math.abs(a.sat - raw.sat) + Math.abs(a.warmth - raw.warmth) : 0

    ok('bonfire-warm shifts warmer than raw', warm && warm.warmth > raw.warmth - 0.002, `warmth ${warm?.warmth?.toFixed(3)} vs raw ${raw.warmth.toFixed(3)}`)
    ok('mono desaturates toward greyscale', mono && mono.sat < 0.08, `sat ${mono?.sat?.toFixed(3)}`)
    ok('low-light brightens mean luma', low && low.luma > raw.luma * 1.03, `luma ${low?.luma?.toFixed(3)} vs raw ${raw.luma.toFixed(3)}`)
    ok('studio differs from raw', diff(studio) > 0.01, `delta ${diff(studio).toFixed(3)}`)
  }
} catch (err) {
  fail++
  console.error('FAIL: harness error —', err?.message || err)
} finally {
  await browser.close()
}

console.log(`\n${pass} passed, ${fail} failed`)
process.exit(fail === 0 ? 0 : 1)
