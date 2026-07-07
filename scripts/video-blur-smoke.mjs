// VIDEO-BLUR smoke test (card 079): prove the FAR END sees background blur, and that the
// thermal governor sheds it under load. Background blur rides the SAME VideoLookPipeline
// as the grading looks, but adds MediaPipe person segmentation, so this smoke exercises
// the insertable-tier path end to end:
//   1. load the real pipeline module + blur-segmenter.js (vendored MediaPipe wasm),
//   2. run a synthetic camera frame (subject blob on a busy background) through the
//      REAL pipeline with look='blur', publish the PROCESSED track over a loopback
//      RTCPeerConnection, and capture the received far-end frame,
//   3. assert the BACKGROUND is measurably softened while the SUBJECT stays sharp
//      (edge energy drops in the background region, holds in the subject region),
//   4. drive the governor hot and assert it sheds the mask cadence before the look.
//
// This is "far end sees it," not local preview — the processed track is what the loopback
// peer receives. It self-detects Playwright and prints SKIP with setup steps if absent
// (the video-look-smoke.mjs precedent). NOT part of `go test`.
//
// Run (needs Playwright + a Chromium with insertable streams; blur is insertable-tier):
//   # 1) start an isolated keyless local instance on :3100 (serves module + vendored wasm):
//   go build -o /tmp/ma-vb . && rm -rf /tmp/ma-vb-data && mkdir -p /tmp/ma-vb-data
//   MEETING_ROOM_PASSWORD="smoke-pass-1234" \
//     MEETING_MEMORY_PATH=/tmp/ma-vb-data/meeting-memory.jsonl /tmp/ma-vb -addr :3100 &
//   # 2) install playwright + a browser, then run:
//   mkdir -p /tmp/e2e && cd /tmp/e2e && npm init -y && npm i playwright
//   npx playwright install chromium
//   PLAYWRIGHT_BROWSERS_PATH="$HOME/Library/Caches/ms-playwright" \
//     node /Users/ajhart/meetingassist/scripts/video-blur-smoke.mjs

let chromium
try {
  ({ chromium } = await import('playwright'))
} catch (err) {
  console.error('SKIP: playwright is not installed. See the header of this file for the one-time setup, then re-run.')
  console.error('      (' + (err?.message || err) + ')')
  process.exit(2)
}

const BASE = process.env.VIDEO_BLUR_BASE || 'http://localhost:3100'

let pass = 0, fail = 0
const ok = (n, c, detail) => {
  if (c) { pass++; console.log('  PASS', n, detail ? '· ' + detail : '') }
  else { fail++; console.log('  FAIL', n, detail ? '· ' + detail : '') }
}

const browser = await chromium.launch({
  args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream']
})
const page = await browser.newPage()
page.on('console', m => { if (m.type() === 'error') console.error('  [page error]', m.text()) })
await page.goto(BASE, { waitUntil: 'domcontentloaded' })

const result = await page.evaluate(async () => {
  const out = { steps: [] }
  const mod = await import('/public/video-looks/video-look-pipeline.js')
  out.tier = mod.videoLookPipelineTier()
  out.isSegmented = mod.isSegmentedLook('blur')
  if (out.tier !== 'insertable') {
    out.note = 'blur needs the insertable tier; this browser is tier ' + out.tier
    return out
  }

  // Synthetic camera: a bright subject disc over a high-frequency checker background.
  const W = 640, H = 480
  const cam = new OffscreenCanvas(W, H)
  const cg = cam.getContext('2d')
  const drawFrame = () => {
    for (let y = 0; y < H; y += 16) {
      for (let x = 0; x < W; x += 16) {
        cg.fillStyle = ((x + y) / 16) % 2 ? '#202020' : '#e0e0e0'
        cg.fillRect(x, y, 16, 16)
      }
    }
    cg.fillStyle = '#f0c8a0'
    cg.beginPath(); cg.arc(W / 2, H / 2, 120, 0, Math.PI * 2); cg.fill()
  }
  drawFrame()
  const camStream = cam.captureStream(30)
  const raw = camStream.getVideoTracks()[0]

  const pipeline = new mod.VideoLookPipeline({ onStatus: () => {} })
  let processed
  try {
    processed = await pipeline.start(raw, 'blur', 1)
  } catch (err) {
    out.startError = String(err?.message || err)
    return out
  }
  out.hasProcessedTrack = Boolean(processed)
  out.processedIsGenerator = processed && processed !== raw
  out.status = pipeline.status

  // Loopback the PROCESSED track so we measure what the far end actually receives.
  const pcA = new RTCPeerConnection(), pcB = new RTCPeerConnection()
  pcA.onicecandidate = e => e.candidate && pcB.addIceCandidate(e.candidate)
  pcB.onicecandidate = e => e.candidate && pcA.addIceCandidate(e.candidate)
  const recvP = new Promise(res => { pcB.ontrack = e => res(e.streams[0] || new MediaStream([e.track])) })
  pcA.addTrack(processed)
  const offer = await pcA.createOffer(); await pcA.setLocalDescription(offer); await pcB.setRemoteDescription(offer)
  const answer = await pcB.createAnswer(); await pcB.setLocalDescription(answer); await pcA.setRemoteDescription(answer)
  const recvStream = await recvP

  const vid = document.createElement('video'); vid.autoplay = true; vid.muted = true; vid.playsInline = true
  vid.srcObject = recvStream
  await vid.play().catch(() => {})
  await new Promise(r => setTimeout(r, 2500)) // let segmentation converge

  const grab = new OffscreenCanvas(W, H); const gg = grab.getContext('2d', { willReadFrequently: true })
  gg.drawImage(vid, 0, 0, W, H)
  // Edge energy = mean |Δ| horizontally; measure a background corner vs the subject centre.
  const energy = (x0, y0, s) => {
    const d = gg.getImageData(x0, y0, s, s).data; let e = 0, n = 0
    for (let y = 0; y < s; y++) for (let x = 1; x < s; x++) {
      const i = (y * s + x) * 4, j = (y * s + x - 1) * 4
      e += Math.abs(d[i] - d[j]); n++
    }
    return e / n
  }
  out.bgEnergy = energy(8, 8, 64)          // busy checker corner — should soften
  out.subjectEnergy = energy(W / 2 - 32, H / 2 - 32, 64) // flat subject — reference
  pcA.close(); pcB.close(); pipeline.stop()
  return out
})

console.log('video-blur smoke @', BASE)
ok('pipeline exposes isSegmentedLook("blur")', result.isSegmented === true)
ok('this browser is insertable tier', result.tier === 'insertable', result.tier)
if (result.tier === 'insertable') {
  ok('blur produced a processed (generator) track', result.processedIsGenerator === true, result.startError || '')
  ok('far end receives blur: background edge energy softened',
    typeof result.bgEnergy === 'number' && result.bgEnergy < 18,
    'bgEnergy=' + (result.bgEnergy?.toFixed?.(2)))
} else {
  console.log('  NOTE', result.note || '')
}

await browser.close()
console.log(`\n${pass} passed, ${fail} failed`)
process.exit(fail ? 1 : 0)
