// MULTI-ENDPOINT smoke test (Spectacular OS Wave 4): the mandated broken case.
// The SAME account joins the room from two separate browser contexts (two
// devices). Before this wave the second join overwrote the single session slot
// and the first device was ejected with `session_replaced`. Now, because each
// context mints its own stable `bonfire.endpoint.id.v1`, both endpoints coexist:
// both stay in the roster and neither is replaced.
//
// Scope note: this asserts each device's OWN local media is rendering — proof
// that both endpoints negotiated real WebRTC through the SFU and neither was
// ejected. It does NOT assert what a THIRD participant sees of a two-device
// account (the client renders one tile per account identity; see the wave notes
// on the single-visible-tile model). observe-cross-participant coverage lives in
// the manual three-context check, not here.
//
// It drives the REAL Go SFU in a REAL browser engine (Chromium), with
// getUserMedia overridden to a live canvas.captureStream() so no physical
// camera is needed but the whole WebRTC + SFU path runs.
//
// Run (needs Playwright + a browser; NOT part of `go test`):
//   # 1) start an isolated keyless local instance on :3100:
//   go build -o /tmp/ma-me . && rm -rf /tmp/ma-me-data && mkdir -p /tmp/ma-me-data
//   MEETING_ROOM_PASSWORD="smoke-pass-1234" \
//     MEETING_MEMORY_PATH=/tmp/ma-me-data/meeting-memory.jsonl /tmp/ma-me -addr :3100 &
//   # 2) install playwright + a browser (temp dir is fine), then run:
//   mkdir -p /tmp/e2e && cd /tmp/e2e && npm init -y && npm i playwright
//   npx playwright install chromium
//   PLAYWRIGHT_BROWSERS_PATH="$HOME/Library/Caches/ms-playwright" \
//     node /Users/ajhart/meetingassist/scripts/multi-endpoint-smoke.mjs
// Seeded accounts (accounts.go) all share MEETING_ROOM_PASSWORD locally.

let chromium
try {
  ({ chromium } = await import('playwright'))
} catch (err) {
  console.error('SKIP: playwright is not installed. See the header of this file for the one-time setup, then re-run.')
  console.error('      (' + (err?.message || err) + ')')
  process.exit(2)
}

const BASE = process.env.MULTI_ENDPOINT_BASE || 'http://localhost:3100'
const PW = process.env.MEETING_ROOM_PASSWORD || 'smoke-pass-1234'
const ACCOUNT = process.env.MULTI_ENDPOINT_ACCOUNT || 'AJ'

let pass = 0, fail = 0
const ok = (n, c) => { if (c) { pass++; console.log('  PASS', n) } else { fail++; console.log('  FAIL', n) } }
const sleep = ms => new Promise(r => setTimeout(r, ms))

// Override getUserMedia with a live canvas + oscillator stream, and sniff the
// room websocket for a `session_replaced` frame so an eviction can never pass
// silently.
function initScript(label) {
  return `(${(label) => {
    window.__LABEL = label
    window.__sessionReplaced = false
    const NativeWS = window.WebSocket
    window.WebSocket = function (...args) {
      const socket = new NativeWS(...args)
      socket.addEventListener('message', ev => {
        try {
          if (typeof ev.data === 'string' && ev.data.indexOf('session_replaced') !== -1) {
            window.__sessionReplaced = true
          }
        } catch (_) {}
      })
      return socket
    }
    window.WebSocket.prototype = NativeWS.prototype
    Object.assign(window.WebSocket, NativeWS)

    const W = 640, H = 480
    function makeStream(text) {
      const c = document.createElement('canvas'); c.width = W; c.height = H
      const x = c.getContext('2d'); let f = 0
      setInterval(() => {
        f++; x.fillStyle = 'hsl(' + ((f * 4) % 360) + ',70%,45%)'; x.fillRect(0, 0, W, H)
        x.fillStyle = '#fff'; x.font = '32px sans-serif'; x.fillText(text + ' ' + f, 16, H / 2)
      }, 33)
      return c.captureStream(30)
    }
    const camStream = makeStream(label)
    function audioTrack() {
      const ac = new (window.AudioContext || window.webkitAudioContext)(); ac.resume && ac.resume()
      const d = ac.createMediaStreamDestination(); const o = ac.createOscillator(); o.frequency.value = 110
      const g = ac.createGain(); g.gain.value = 0.001; o.connect(g); g.connect(d); o.start()
      return d.stream.getAudioTracks()[0]
    }
    navigator.mediaDevices.getUserMedia = async (constraints) => {
      const tracks = []
      if (constraints && constraints.video) tracks.push(camStream.getVideoTracks()[0])
      if (constraints && constraints.audio) tracks.push(audioTrack())
      return new MediaStream(tracks)
    }
    navigator.mediaDevices.enumerateDevices = async () => ([
      { deviceId: 'cam', kind: 'videoinput', label: 'fake cam', groupId: 'g' },
      { deviceId: 'mic', kind: 'audioinput', label: 'fake mic', groupId: 'g' },
    ])
  }})(${JSON.stringify(label)})`
}

// Join the room as `label` in its own browser context (own localStorage → own
// endpoint id). `tag` distinguishes the two devices in the logs.
async function join(browser, label, tag) {
  const context = await browser.newContext({ permissions: ['camera', 'microphone'] })
  const resp = await context.request.post(BASE + '/auth/login', { data: { name: label, password: PW } })
  if (!resp.ok()) throw new Error(`${label}/${tag} login failed: ${resp.status()}`)
  const page = await context.newPage()
  page.on('console', m => {
    const t = m.text()
    if (/session_replaced|replaced/i.test(t)) console.log(`   [${tag} console] ${t.slice(0, 160)}`)
  })
  await page.addInitScript(initScript(`${label}`))
  await page.goto(BASE, { waitUntil: 'domcontentloaded', timeout: 60000 })
  await page.waitForFunction(() => typeof joinRoom === 'function' && typeof setActiveTool === 'function', null, { timeout: 30000 })
  await page.selectOption('#loginAccountSelect', label).catch(() => {})
  await page.fill('#roomPassword', PW).catch(() => {})
  await page.evaluate(async () => { setActiveTool('room'); await joinRoom() })
  return { context, page, label, tag }
}

// The endpoint id is minted when the room websocket opens (socket.onopen), which
// can lag joinRoom()'s return by a few ms — so read it after a settle, not
// inline with the join, to avoid a false empty read.
const endpointIdOf = (s) => s.page.evaluate(() => {
  try { return localStorage.getItem('bonfire.endpoint.id.v1') } catch (_) { return null }
})

const rosterNames = (s) => s.page.evaluate(() =>
  [...document.querySelectorAll('#videoStack .video-tile')].map(t => t.dataset.participant).filter(Boolean))

const sessionReplaced = (s) => s.page.evaluate(() => window.__sessionReplaced === true)

const stillInRoom = (s) => s.page.evaluate(() => document.getElementById('appShell')?.classList.contains('is-in-room') === true)

const liveVideoCount = (s) => s.page.evaluate(() =>
  [...document.querySelectorAll('video')].filter(v => v.srcObject &&
    v.srcObject.getVideoTracks().some(t => t.readyState === 'live') && v.videoWidth > 0).length)

const browser = await chromium.launch()
const devices = []
try {
  console.log(`[setup] account "${ACCOUNT}" joins from TWO devices (two contexts) against ${BASE}`)
  const laptop = await join(browser, ACCOUNT, 'laptop')
  devices.push(laptop)
  await sleep(1500)
  const phone = await join(browser, ACCOUNT, 'phone')
  devices.push(phone)

  // Let both fully negotiate and the roster propagate (and the endpoint ids land).
  await sleep(6000)

  const laptopEndpointId = await endpointIdOf(laptop)
  const phoneEndpointId = await endpointIdOf(phone)
  console.log(`   laptop endpointId=${laptopEndpointId}`)
  console.log(`   phone  endpointId=${phoneEndpointId}`)
  ok('the two devices minted DISTINCT endpoint ids', !!laptopEndpointId && !!phoneEndpointId && laptopEndpointId !== phoneEndpointId)

  ok('laptop was NOT told session_replaced when the phone joined', (await sessionReplaced(laptop)) === false)
  ok('phone was NOT told session_replaced', (await sessionReplaced(phone)) === false)
  ok('laptop is still in the room right after the phone joined', (await stillInRoom(laptop)) === true)
  ok('phone is in the room', (await stillInRoom(phone)) === true)

  // Hold for >30s and re-check that neither device was quietly evicted.
  console.log('[hold] keeping both devices in the room for 32s...')
  const start = Date.now()
  let laptopReplaced = false, phoneReplaced = false
  while (Date.now() - start < 32000) {
    await sleep(2000)
    if (await sessionReplaced(laptop)) laptopReplaced = true
    if (await sessionReplaced(phone)) phoneReplaced = true
    if (laptopReplaced || phoneReplaced) break
  }
  ok('no session_replaced arrived on the laptop across 30s+', laptopReplaced === false)
  ok('no session_replaced arrived on the phone across 30s+', phoneReplaced === false)
  ok('laptop is STILL in the room after 30s+', (await stillInRoom(laptop)) === true)
  ok('phone is STILL in the room after 30s+', (await stillInRoom(phone)) === true)

  const laptopVideos = await liveVideoCount(laptop)
  const phoneVideos = await liveVideoCount(phone)
  console.log(`   live rendering <video> — laptop=${laptopVideos}, phone=${phoneVideos}`)
  ok('laptop has at least its own live video rendering', laptopVideos > 0)
  ok('phone has at least its own live video rendering', phoneVideos > 0)

  const laptopRoster = await rosterNames(laptop)
  const phoneRoster = await rosterNames(phone)
  console.log('   laptop roster tiles:', JSON.stringify(laptopRoster))
  console.log('   phone  roster tiles:', JSON.stringify(phoneRoster))
  ok('both contexts render the shared account identity in the roster',
    laptopRoster.some(n => n && n.toLowerCase() === ACCOUNT.toLowerCase()) &&
    phoneRoster.some(n => n && n.toLowerCase() === ACCOUNT.toLowerCase()))
} finally {
  for (const d of devices) { await d.context.close().catch(() => {}) }
  await browser.close().catch(() => {})
}

console.log(`\n[result] ${pass} passed, ${fail} failed`)
process.exit(fail === 0 ? 0 : 1)
