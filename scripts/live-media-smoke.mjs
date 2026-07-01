#!/usr/bin/env node

import { spawn } from 'node:child_process'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, isAbsolute, join, resolve } from 'node:path'

const defaults = {
  chromePath: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  password: process.env.ROOM_PASSWORD || 'B0NFIRE!',
  participants: ['Tom', 'Caitlyn'],
  externalParticipants: [],
  rejoin: '',
  separateBrowsers: true,
  durationMs: 0,
  emitReport: '',
  interactive: false,
  lateJoin: '',
  mobileViewport: null,
  timeoutMs: 45000,
  url: 'https://thebonfire.xyz'
}

const options = parseArgs(process.argv.slice(2))
const config = {
  ...defaults,
  ...options,
  participants: options.participants || defaults.participants,
  externalParticipants: options.externalParticipants || defaults.externalParticipants
}
const lateJoinPlan = resolveLateJoinPlan(config)
const initialParticipantNames = config.participants.filter(name => !(lateJoinPlan.type === 'automated' && name === lateJoinPlan.name))
const initialExternalParticipantNames = config.externalParticipants.filter(name => !(lateJoinPlan.type === 'external' && name === lateJoinPlan.name))
const reportStartedAt = new Date().toISOString()

const pages = []
const browsers = []
let sharedBrowser = null

let shuttingDown = false
process.on('SIGINT', () => finish(130))
process.on('SIGTERM', () => finish(143))

try {
  if (lateJoinPlan.name) {
    log('late-join-plan', lateJoinPlan.type, lateJoinPlan.name)
  }
  if (config.externalParticipants.length) {
    log('external-participants', config.externalParticipants.join(','))
  }
  if (config.mobileViewport) {
    log('mobile-viewport', `${config.mobileViewport.width}x${config.mobileViewport.height}`, config.mobileViewport.orientation)
  }

  if (!config.separateBrowsers) {
    sharedBrowser = await createBrowser('shared')
  }

  for (const name of initialParticipantNames) {
    const browser = config.separateBrowsers ? await createBrowser(name) : sharedBrowser
    pages.push(await openPage(name, browser))
  }

  for (const page of pages) {
    await joinRoom(page)
  }

  await waitForExpectedRoomMedia(expectedParticipantNames({ includeLate: false }))

  const lateJoinSnapshots = lateJoinPlan.name ? await performLateJoin(lateJoinPlan) : []
  const expectedNames = expectedParticipantNames({ includeLate: true })

  const recordingSnapshots = await exerciseRecordingToggle(pages[0])

  if (config.rejoin) {
    await rejoinParticipant(config.rejoin)
    await waitForExpectedRoomMedia(expectedNames)
  }

  const screenShareSnapshots = await exerciseScreenShare(pages[0])

  await sleep(6000)
  for (const page of pages) {
    const stageTarget = expectedNames.find(name => name !== page.name) || page.name
    await showStageView(page, stageTarget)
  }
  await sleep(5000)
  const stageSnapshots = await collectSnapshots('stage')
  let mobileStageDrawerSnapshots = []
  if (config.mobileViewport) {
    for (const page of pages) {
      await openStageParticipantsDrawer(page)
    }
    await sleep(1000)
    mobileStageDrawerSnapshots = await collectSnapshots('mobile-stage-drawer')
  }

  for (const page of pages) {
    await showExpandedBoardView(page)
  }
  await sleep(5000)
  const boardSnapshots = await collectSnapshots('board')
  const soakSnapshots = await collectSoakSnapshots(expectedNames)
  const postLeaveParticipants = await closePagesAndCollectParticipants(pages)

  const failures = [
    ...validateRecordingSnapshots(recordingSnapshots.off, 'off'),
    ...validateRecordingSnapshots(recordingSnapshots.on, 'on'),
    ...validateLateJoinSnapshots(lateJoinSnapshots, expectedNames),
    ...validateScreenShareSnapshots(screenShareSnapshots.started, pages[0].name, expectedNames),
    ...validateScreenShareStoppedSnapshots(screenShareSnapshots.stopped, pages[0].name),
    ...validateSnapshots(stageSnapshots, expectedNames.length, { view: 'stage', requireStageView: true, expectedNames }),
    ...validateMobileStageDrawerSnapshots(mobileStageDrawerSnapshots),
    ...validateSnapshots(boardSnapshots, expectedNames.length, { view: 'board', requireBoardDock: true, expectedNames }),
    ...validateSnapshots(soakSnapshots, expectedNames.length, { view: 'soak', requireBoardDock: true, expectedNames }),
    ...validatePostLeaveParticipants(postLeaveParticipants)
  ]
  const result = { ok: failures.length === 0, failures, snapshots: boardSnapshots, stageSnapshots, mobileStageDrawerSnapshots, screenShareSnapshots, recordingSnapshots, lateJoinSnapshots, soakSnapshots, postLeaveParticipants }
  await emitReport(result)
  console.log(JSON.stringify(result, null, 2))
  await finish(failures.length === 0 ? 0 : 1)
} catch (error) {
  const failures = [error?.stack || error?.message || String(error)]
  const failureSnapshots = []
  if (pages.length) {
    for (const page of pages) {
      try {
        const snapshot = await basicPageState(page)
        failureSnapshots.push({ name: page.name, snapshot })
        log('failure-snapshot', page.name, JSON.stringify(snapshot))
      } catch (snapshotError) {
        log('failure-snapshot-error', page.name, snapshotError.message)
      }
      if (page.events.length) {
        log('page-events', page.name, JSON.stringify(page.events.slice(-12)))
      }
    }
  }
  try {
    for (const browser of browsers) {
      const targets = await fetchJSON(`http://127.0.0.1:${browser.debugPort}/json/list`)
      log('open-targets', browser.label, JSON.stringify(targets.map(target => ({ id: target.id, url: target.url, title: target.title }))))
    }
  } catch {
    // DevTools may already be down.
  }
  await emitReport({ ok: false, failures, failureSnapshots })
  console.error('live media smoke failed:', error?.stack || error?.message || error)
  await finish(1)
}

function parseArgs(args) {
  const parsed = {}
  for (let index = 0; index < args.length; index++) {
    const arg = args[index]
    const next = args[index + 1]
    if (arg === '--url' && next) {
      parsed.url = next
      index++
    } else if (arg === '--password' && next) {
      parsed.password = next
      index++
    } else if (arg === '--participants' && next) {
      parsed.participants = next.split(',').map(value => value.trim()).filter(Boolean)
      index++
    } else if (arg === '--external-participant' && next) {
      parsed.externalParticipants = [
        ...(parsed.externalParticipants || []),
        ...next.split(',').map(value => value.trim()).filter(Boolean)
      ]
      index++
    } else if (arg === '--rejoin' && next) {
      parsed.rejoin = next.trim()
      index++
    } else if (arg === '--late-join') {
      if (next && !next.startsWith('--')) {
        parsed.lateJoin = next.trim() || true
        index++
      } else {
        parsed.lateJoin = true
      }
    } else if (arg === '--chrome' && next) {
      parsed.chromePath = next
      index++
    } else if (arg === '--separate-browsers') {
      parsed.separateBrowsers = true
    } else if (arg === '--interactive') {
      parsed.interactive = true
    } else if (arg === '--duration-ms' && next) {
      parsed.durationMs = Math.max(0, Number(next) || 0)
      index++
    } else if (arg === '--emit-report') {
      if (next && !next.startsWith('--')) {
        parsed.emitReport = next
        index++
      } else {
        parsed.emitReport = true
      }
    } else if (arg === '--mobile-viewport') {
      if (next && !next.startsWith('--')) {
        parsed.mobileViewport = parseMobileViewport(next)
        index++
      } else {
        parsed.mobileViewport = parseMobileViewport('')
      }
    } else if (arg === '--timeout-ms' && next) {
      parsed.timeoutMs = Number(next) || defaults.timeoutMs
      index++
    }
  }
  return parsed
}

function parseMobileViewport(value) {
  const defaults = {
    width: 390,
    height: 844,
    deviceScaleFactor: 3,
    orientation: 'portrait'
  }
  const raw = String(value || '').trim().toLowerCase()
  if (!raw || raw === 'phone' || raw === 'mobile' || raw === 'iphone') {
    return defaults
  }
  if (raw === 'landscape') {
    return { ...defaults, width: 844, height: 390, orientation: 'landscape' }
  }
  const match = raw.match(/^(\d{3,4})x(\d{3,4})(?:@([1-9](?:\.\d+)?))?(?::(portrait|landscape))?$/)
  if (!match) {
    log('mobile-viewport-warning', `could not parse ${JSON.stringify(value)}, using 390x844 portrait`)
    return defaults
  }
  const width = Number(match[1])
  const height = Number(match[2])
  return {
    width,
    height,
    deviceScaleFactor: Number(match[3]) || defaults.deviceScaleFactor,
    orientation: match[4] || (width > height ? 'landscape' : 'portrait')
  }
}

function resolveLateJoinPlan(config) {
  if (!config.lateJoin) {
    return { name: '', type: '' }
  }
  const requestedName = config.lateJoin === true ? '' : String(config.lateJoin || '').trim()
  const name = requestedName || config.participants[config.participants.length - 1] || config.externalParticipants[config.externalParticipants.length - 1] || ''
  if (!name) {
    return { name: '', type: '' }
  }
  if (config.externalParticipants.includes(name)) {
    return { name, type: 'external' }
  }
  if (!config.participants.includes(name)) {
    config.participants = [...config.participants, name]
  }
  const remainingAutomatedParticipants = config.participants.filter(participantName => participantName !== name)
  if (remainingAutomatedParticipants.length === 0) {
    throw new Error(`--late-join ${name} would leave no automated participant to hold the room`)
  }
  return { name, type: 'automated' }
}

function expectedParticipantNames({ includeLate }) {
  const automatedNames = includeLate
    ? config.participants
    : initialParticipantNames
  const externalNames = includeLate
    ? config.externalParticipants
    : initialExternalParticipantNames
  return uniqueNames([...automatedNames, ...externalNames])
}

function uniqueNames(names) {
  return Array.from(new Set(names.map(name => String(name || '').trim()).filter(Boolean)))
}

async function findDebugPort() {
  for (let attempt = 0; attempt < 20; attempt++) {
    const port = 9600 + Math.floor(Math.random() * 700)
    try {
      const server = await import('node:net').then(({ createServer }) => createServer())
      await new Promise((resolve, reject) => {
        server.once('error', reject)
        server.listen(port, '127.0.0.1', resolve)
      })
      await new Promise(resolve => server.close(resolve))
      return port
    } catch {
      // Try another port.
    }
  }
  throw new Error('could not reserve a Chrome debugging port')
}

async function createBrowser(label) {
  const userDataDir = await mkdtemp(join(tmpdir(), 'meetingassist-live-smoke-'))
  const debugPort = await findDebugPort()
  const chromeArgs = [
    `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${userDataDir}`,
    '--use-fake-device-for-media-stream',
    '--use-fake-ui-for-media-stream',
    '--autoplay-policy=no-user-gesture-required',
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-features=MediaRouter',
    'about:blank'
  ]
  if (!config.interactive) {
    chromeArgs.unshift('--headless=new')
  }
  const chrome = spawn(config.chromePath, chromeArgs, { stdio: ['ignore', 'ignore', 'pipe'] })
  chrome.stderr.on('data', data => {
    for (const line of String(data).split('\n')) {
      if (line.includes('DevTools listening') || line.includes('ERROR')) {
        console.error(line)
      }
    }
  })
  const browser = { chrome, debugPort, userDataDir, label, closed: false }
  browsers.push(browser)
  const version = await waitForChrome(browser)
  log('chrome', label, version.Browser)
  return browser
}

async function waitForChrome(browser) {
  for (let attempt = 0; attempt < 120; attempt++) {
    try {
      return await fetchJSON(`http://127.0.0.1:${browser.debugPort}/json/version`)
    } catch {
      await sleep(250)
    }
  }
  throw new Error('Chrome did not expose DevTools')
}

async function openPage(name, browser) {
  const target = await fetchJSON(`http://127.0.0.1:${browser.debugPort}/json/new?about:blank`, { method: 'PUT' })
  const client = connectCDP(target.webSocketDebuggerUrl)
  await client.opened
  await client.send('Runtime.enable')
  await client.send('Page.enable')
  const page = { name, client, events: [], browser }
  await applyPageEmulation(page)
  client.on('Runtime.consoleAPICalled', event => {
    page.events.push({
      type: 'console',
      level: event.type,
      text: (event.args || []).map(arg => arg.value || arg.description || '').join(' ').slice(0, 500)
    })
  })
  client.on('Runtime.exceptionThrown', event => {
    page.events.push({
      type: 'exception',
      text: event.exceptionDetails?.exception?.description || event.exceptionDetails?.text || ''
    })
  })
  await client.send('Page.navigate', { url: config.url })
  log('page', name, target.id)
  return page
}

async function applyPageEmulation(page) {
  if (!config.mobileViewport) {
    return
  }
  const viewport = config.mobileViewport
  await page.client.send('Emulation.setDeviceMetricsOverride', {
    width: viewport.width,
    height: viewport.height,
    deviceScaleFactor: viewport.deviceScaleFactor,
    mobile: true,
    screenWidth: viewport.width,
    screenHeight: viewport.height,
    screenOrientation: viewport.orientation === 'landscape'
      ? { type: 'landscapePrimary', angle: 90 }
      : { type: 'portraitPrimary', angle: 0 }
  })
  await page.client.send('Emulation.setTouchEmulationEnabled', {
    enabled: true,
    maxTouchPoints: 5
  })
}

async function joinRoom(page) {
  await waitFor(page, `${page.name} form`, `
    Boolean(document.readyState !== 'loading'
      && typeof joinRoom === 'function'
      && typeof signInToOffice === 'function'
      && document.getElementById('loginAccountSelect')
      && document.getElementById('participantName')
      && document.getElementById('roomPassword')
      && document.getElementById('joinAccess'))
  `)
  await evaluate(page, `
    (() => {
      const account = document.getElementById('loginAccountSelect')
      const participant = document.getElementById('participantName')
      const password = document.getElementById('roomPassword')
      account.value = ${JSON.stringify(page.name)}
      account.dispatchEvent(new Event('change', { bubbles: true }))
      participant.value = ${JSON.stringify(page.name)}
      participant.dispatchEvent(new Event('change', { bubbles: true }))
      password.value = ${JSON.stringify(config.password)}
      password.dispatchEvent(new Event('input', { bubbles: true }))
      return true
    })()
  `)
  const signedIn = await evaluate(page, 'signInToOffice()')
  if (!signedIn) {
    throw new Error(`${page.name} could not sign in to office`)
  }
  await waitFor(page, `${page.name} office session`, `
    (() => {
      const hasLocalMedia = typeof localStream !== 'undefined' && localStream
      const hasOfficeSession = typeof authedUser !== 'undefined'
        && authedUser
        && (document.querySelector('[data-tool="room"]') || document.querySelector('[data-join-room]'))
      return Boolean(hasLocalMedia || hasOfficeSession)
    })()
  `)
  const hasLocalMedia = await evaluate(page, `
    (() => Boolean(typeof localStream !== 'undefined' && localStream))()
  `)
  if (!hasLocalMedia) {
    await evaluate(page, `
      (async () => {
        setActiveTool?.('room')
        await joinRoom()
        return true
      })()
    `)
  }
  await waitFor(page, `${page.name} local media`, `
    (() => typeof localStream !== 'undefined'
      && localStream
      && localStream.getAudioTracks().length >= 1
      && localStream.getVideoTracks().length >= 1)()
  `)
  await waitFor(page, `${page.name} room state`, `
    (() => typeof pc !== 'undefined'
      && pc
      && typeof currentParticipantName !== 'undefined'
      && currentParticipantName === ${JSON.stringify(page.name)})()
  `)
}

async function waitForExpectedRoomMedia(expectedNames) {
  if (expectedNames.length > pages.length) {
    const externalNames = expectedNames.filter(name => !pages.some(page => page.name === name))
    log('waiting-for-external-participants', externalNames.join(','))
  }
  for (const page of pages) {
    await waitForRoomMedia(page, expectedNames)
  }
}

async function waitForRoomMedia(page, expectedNames) {
  const expectedClientCount = expectedNames.length
  await waitFor(page, `${page.name} remote media`, `
    (() => {
      const remoteAudioCount = typeof remoteAudioMonitors === 'function'
        ? remoteAudioMonitors().length
        : (typeof audioMonitors !== 'undefined' ? audioMonitors.size : 0)
      return typeof remoteElements !== 'undefined' && remoteElements.size >= ${expectedClientCount - 1}
        && remoteAudioCount >= ${expectedClientCount - 1}
    })()
  `)
  await waitFor(page, `${page.name} participant labels`, `
    (() => {
      const expected = ${JSON.stringify(expectedNames)}
      const participants = typeof participantsInRoom !== 'undefined' ? participantsInRoom : []
      const labels = Array.from(document.querySelectorAll('.video-tile'))
        .map(tile => tile.dataset.participant || '')
        .filter(Boolean)
      return expected.every(name => participants.includes(name))
        && expected.every(name => labels.filter(label => label === name).length === 1)
        && !labels.includes('participant')
    })()
  `)
}

async function rejoinParticipant(name) {
  const pageIndex = pages.findIndex(page => page.name === name)
  if (pageIndex === -1) {
    throw new Error(`cannot rejoin unknown participant ${name}`)
  }

  const oldPage = pages[pageIndex]
  log('rejoin-start', name)
  await evaluate(oldPage, 'leaveRoom?.(); true').catch(() => {})
  oldPage.client.close()
  if (config.separateBrowsers) {
    await closeBrowser(oldPage.browser)
  }
  pages.splice(pageIndex, 1)
  await sleep(2200)

  const browser = config.separateBrowsers ? await createBrowser(name) : (oldPage.browser || sharedBrowser)
  const newPage = await openPage(name, browser)
  pages.splice(pageIndex, 0, newPage)
  await joinRoom(newPage)
  log('rejoin-complete', name)
}

async function performLateJoin(plan) {
  log('late-join-start', plan.type, plan.name)
  if (plan.type === 'automated') {
    const browser = config.separateBrowsers ? await createBrowser(plan.name) : sharedBrowser
    const page = await openPage(plan.name, browser)
    pages.push(page)
    await joinRoom(page)
  } else {
    log('late-join-external-wait', `${plan.name} should join ${config.url}`)
  }
  const expectedNames = expectedParticipantNames({ includeLate: true })
  await waitForExpectedRoomMedia(expectedNames)
  await sleep(2000)
  const snapshots = await collectSnapshots('late-join')
  log('late-join-complete', plan.type, plan.name)
  return snapshots
}

async function exerciseRecordingToggle(controller) {
  if (!controller) {
    return { off: [], on: [] }
  }
  await evaluate(controller, `
    (() => {
      document.getElementById('recordMeeting')?.click()
      return true
    })()
  `)
  for (const page of pages) {
    await waitFor(page, `${page.name} record off status`, `
      (() => document.getElementById('statusText')?.textContent?.trim() === 'the room is not listening'
        && !document.getElementById('statusPill')?.classList.contains('pill--listening'))()
    `)
  }
  const off = await collectRecordingSnapshots()

  await evaluate(controller, `
    (() => {
      document.getElementById('recordMeeting')?.click()
      return true
    })()
  `)
  for (const page of pages) {
    await waitFor(page, `${page.name} record on status`, `
      (() => document.getElementById('statusText')?.textContent?.trim() === 'the room is listening'
        && document.getElementById('statusPill')?.classList.contains('pill--listening'))()
    `)
  }
  const on = await collectRecordingSnapshots()
  return { off, on }
}

async function exerciseScreenShare(sharer) {
  if (!sharer) {
    return { started: [], stopped: [] }
  }
  if (config.mobileViewport) {
    log('screen-share-skip', 'mobile viewport smoke does not initiate fake display capture')
    return { started: [], stopped: [], skipped: true }
  }
  await installFakeDisplayMedia(sharer)
  await evaluate(sharer, `
    (async () => {
      await startScreenShare()
      return Boolean(screenShareStream)
    })()
  `)
  await waitFor(sharer, `${sharer.name} local screen share`, `
    (() => Boolean(screenShareStream
      && activeScreenShareParticipant === currentParticipantName
      && document.getElementById('presentationTile')?.classList.contains('is-screen-sharing')))()
  `)
  for (const page of pages) {
    await waitFor(page, `${page.name} screen share visible`, `
      (() => {
        const stage = document.getElementById('presentationTile')
        const video = document.getElementById('screenStageVideo')
        const stripTiles = Array.from(document.querySelectorAll('#presentationTile.is-screen-sharing #videoStack > .video-tile:not(.is-sharing-screen)'))
        return typeof activeScreenShareParticipant !== 'undefined'
          && activeScreenShareParticipant === ${JSON.stringify(sharer.name)}
          && stage?.classList.contains('is-screen-sharing')
          && video?.srcObject?.getVideoTracks?.().some(track => track.readyState !== 'ended')
          && stripTiles.length >= ${Math.max(0, pages.length - 1)}
      })()
    `)
  }
  await sleep(3500)
  const started = await collectSnapshots('screen-share-started')

  await evaluate(sharer, `
    (async () => {
      await stopScreenShare()
      return true
    })()
  `)
  for (const page of pages) {
    await waitFor(page, `${page.name} screen share stopped`, `
      (() => !document.getElementById('presentationTile')?.classList.contains('is-screen-sharing')
        && !activeScreenShareParticipant)()
    `)
  }
  await sleep(3500)
  const stopped = await collectSnapshots('screen-share-stopped')
  return { started, stopped }
}

async function collectRecordingSnapshots() {
  const snapshots = []
  for (const page of pages) {
    snapshots.push(await evaluate(page, `
      (() => ({
        name: ${JSON.stringify(page.name)},
        statusText: document.getElementById('statusText')?.textContent?.trim() || '',
        statusClass: document.getElementById('statusPill')?.className || '',
        recordLabel: document.querySelector('#recordMeeting .record-label')?.textContent?.trim() || '',
        recordPressed: document.getElementById('recordMeeting')?.getAttribute('aria-pressed') || ''
      }))()
    `))
  }
  return snapshots
}

async function installFakeDisplayMedia(page) {
  await evaluate(page, `
    (() => {
      navigator.mediaDevices.getDisplayMedia = async () => {
        const canvas = document.createElement('canvas')
        canvas.width = 1280
        canvas.height = 720
        const ctx = canvas.getContext('2d')
        let frame = 0
        const draw = () => {
          frame += 1
          const hue = (frame * 7) % 360
          ctx.fillStyle = 'hsl(' + hue + ' 70% 18%)'
          ctx.fillRect(0, 0, canvas.width, canvas.height)
          ctx.fillStyle = 'hsl(' + ((hue + 80) % 360) + ' 80% 56%)'
          ctx.fillRect((frame * 18) % canvas.width, 96, 180, 180)
          ctx.fillStyle = '#fff'
          ctx.font = '42px sans-serif'
          ctx.fillText('screen share ' + frame, 80, 620)
        }
        draw()
        const timer = window.setInterval(draw, 66)
        const stream = canvas.captureStream(15)
        const track = stream.getVideoTracks()[0]
        if (track) {
          const stop = track.stop.bind(track)
          track.stop = () => {
            window.clearInterval(timer)
            stop()
          }
        }
        return stream
      }
      return true
    })()
  `)
}

async function showStageView(page, targetName = '') {
  await evaluate(page, `
    (() => {
      const target = ${JSON.stringify(targetName)}
      if (target && typeof togglePinnedSpeaker === 'function') {
        const current = typeof stageParticipantDisplayName === 'function' ? stageParticipantDisplayName() : ''
        if (current !== target) {
          togglePinnedSpeaker(target)
        }
      } else if (typeof setStageMode === 'function') {
        setStageMode('stage')
      }
      if (typeof repairAuxiliaryVideoPlayback === 'function') {
        repairAuxiliaryVideoPlayback('live media smoke stage view')
      }
      return true
    })()
  `)
}

async function openStageParticipantsDrawer(page) {
  await evaluate(page, `
    (() => {
      const toggle = document.getElementById('stageParticipantsToggle')
      if (toggle && !toggle.hidden && toggle.getAttribute('aria-expanded') !== 'true') {
        toggle.click()
      }
      return true
    })()
  `)
}

async function showExpandedBoardView(page) {
  await waitFor(page, `${page.name} board ready`, `
    (() => typeof isBoardReady !== 'undefined' && isBoardReady)()
  `)
  await evaluate(page, `
    (() => {
      if (typeof setBoardExpanded === 'function') {
        setBoardExpanded(true)
      }
      if (typeof repairAuxiliaryVideoPlayback === 'function') {
        repairAuxiliaryVideoPlayback('live media smoke board view')
      }
      return true
    })()
  `)
}

async function collectSnapshots(phase = 'snapshot') {
  const snapshots = []
  const capturedAt = new Date().toISOString()
  for (const page of pages) {
    snapshots.push({
      phase,
      capturedAt,
      ...(await snapshotPage(page))
    })
  }
  return snapshots
}

async function collectSoakSnapshots(expectedNames) {
  if (!config.durationMs) {
    return []
  }
  const startedAt = Date.now()
  const snapshots = []
  const intervalMs = Math.min(5000, Math.max(1000, Math.floor(config.durationMs / 3) || 1000))
  let iteration = 0
  while (Date.now() - startedAt <= config.durationMs) {
    iteration += 1
    const elapsedMs = Date.now() - startedAt
    log('soak-snapshot', iteration, `${elapsedMs}ms/${config.durationMs}ms`)
    const batch = await collectSnapshots(`soak-${iteration}`)
    for (const snapshot of batch) {
      snapshots.push({
        ...snapshot,
        soakIteration: iteration,
        soakElapsedMs: elapsedMs,
        expectedParticipants: expectedNames
      })
    }
    const remainingMs = config.durationMs - (Date.now() - startedAt)
    if (remainingMs <= 0) {
      break
    }
    await sleep(Math.min(intervalMs, remainingMs))
  }
  return snapshots
}

async function snapshotPage(page) {
  return evaluate(page, `
    (async () => {
      const rectProbe = element => {
        if (!element) {
          return null
        }
        const rect = element.getBoundingClientRect()
        const style = window.getComputedStyle(element)
        return {
          isConnected: Boolean(element.isConnected),
          inDocument: document.contains(element),
          clientRects: element.getClientRects().length,
          rect: {
            x: rect.x,
            y: rect.y,
            top: rect.top,
            left: rect.left,
            right: rect.right,
            bottom: rect.bottom,
            width: rect.width,
            height: rect.height
          },
          display: style.display,
          visibility: style.visibility,
          opacity: style.opacity,
          pointerEvents: style.pointerEvents,
          position: style.position,
          overflow: style.overflow,
          zIndex: style.zIndex
        }
      }
      const trackProbe = track => ({
        id: track.id || '',
        kind: track.kind || '',
        label: track.label || '',
        enabled: Boolean(track.enabled),
        muted: Boolean(track.muted),
        readyState: track.readyState || '',
        settings: track.getSettings?.() || {},
        constraints: track.getConstraints?.() || {}
      })
      const videoProbe = video => {
        if (!video) {
          return null
        }
        let frames = 0
        if (typeof video.getVideoPlaybackQuality === 'function') {
          frames = Number(video.getVideoPlaybackQuality().totalVideoFrames) || 0
        } else {
          frames = Number(video.webkitDecodedFrameCount) || 0
        }
        return {
          id: video.id || '',
          className: video.className || '',
          isConnected: Boolean(video.isConnected),
          inDocument: document.contains(video),
          hidden: Boolean(video.hidden),
          visible: Boolean(video.getClientRects().length),
          rect: rectProbe(video),
          paused: Boolean(video.paused),
          ended: Boolean(video.ended),
          muted: Boolean(video.muted),
          autoplay: Boolean(video.autoplay),
          playsInline: Boolean(video.playsInline),
          currentTime: Number(video.currentTime) || 0,
          readyState: video.readyState,
          videoWidth: video.videoWidth,
          videoHeight: video.videoHeight,
          frames,
          srcObjectTracks: video.srcObject?.getTracks?.().map(trackProbe) || [],
          hasLiveVideo: Boolean(video.srcObject?.getVideoTracks?.().some(track => track.readyState !== 'ended'))
        }
      }
      const canvasProbe = canvas => {
        if (!canvas) {
          return null
        }
        return {
          id: canvas.id || '',
          className: canvas.className || '',
          isConnected: Boolean(canvas.isConnected),
          inDocument: document.contains(canvas),
          hidden: Boolean(canvas.hidden),
          visible: Boolean(canvas.getClientRects().length),
          width: canvas.width || 0,
          height: canvas.height || 0,
          rect: rectProbe(canvas)
        }
      }
      const mediaElementForValue = value => {
        if (!value) {
          return null
        }
        if (value instanceof HTMLMediaElement) {
          return value
        }
        if (value.video instanceof HTMLMediaElement) {
          return value.video
        }
        if (value.videoElement instanceof HTMLMediaElement) {
          return value.videoElement
        }
        if (value.element instanceof HTMLMediaElement) {
          return value.element
        }
        return null
      }
      const stats = typeof pc !== 'undefined' && pc
        ? Array.from((await pc.getStats()).values()).map(stat => ({
            id: stat.id || '',
            type: stat.type,
            kind: stat.kind || '',
            state: stat.state || '',
            nominated: Boolean(stat.nominated),
            selectedCandidatePairId: stat.selectedCandidatePairId || '',
            localCandidateId: stat.localCandidateId || '',
            remoteCandidateId: stat.remoteCandidateId || '',
            currentRoundTripTime: Number(stat.currentRoundTripTime) || 0,
            jitter: Number(stat.jitter) || 0,
            packetsLost: Number(stat.packetsLost) || 0,
            packetsReceived: Number(stat.packetsReceived) || 0,
            framesDecoded: Number(stat.framesDecoded) || 0,
            framesDropped: Number(stat.framesDropped) || 0,
            keyFramesDecoded: Number(stat.keyFramesDecoded) || 0,
            framesEncoded: Number(stat.framesEncoded) || 0,
            framesSent: Number(stat.framesSent) || 0,
            keyFramesEncoded: Number(stat.keyFramesEncoded) || 0,
            frameWidth: Number(stat.frameWidth) || 0,
            frameHeight: Number(stat.frameHeight) || 0,
            bytesReceived: Number(stat.bytesReceived) || 0,
            bytesSent: Number(stat.bytesSent) || 0,
            codecId: stat.codecId || '',
            mimeType: stat.mimeType || '',
            payloadType: Number(stat.payloadType) || 0,
            sdpFmtpLine: stat.sdpFmtpLine || '',
            candidateType: stat.candidateType || '',
            protocol: stat.protocol || '',
            networkType: stat.networkType || ''
          }))
        : []
      const remoteMonitorList = typeof remoteAudioMonitors === 'function'
        ? remoteAudioMonitors()
        : (typeof audioMonitors !== 'undefined'
          ? Array.from(audioMonitors.values()).filter(monitor => typeof isCurrentParticipantName !== 'function' || !isCurrentParticipantName(monitor.name))
          : [])
      return {
        name: ${JSON.stringify(page.name)},
        viewport: {
          innerWidth: window.innerWidth,
          innerHeight: window.innerHeight,
          outerWidth: window.outerWidth,
          outerHeight: window.outerHeight,
          devicePixelRatio: window.devicePixelRatio,
          visualViewport: window.visualViewport ? {
            width: window.visualViewport.width,
            height: window.visualViewport.height,
            scale: window.visualViewport.scale,
            offsetLeft: window.visualViewport.offsetLeft,
            offsetTop: window.visualViewport.offsetTop,
            pageLeft: window.visualViewport.pageLeft,
            pageTop: window.visualViewport.pageTop
          } : null,
          screen: {
            width: window.screen?.width || 0,
            height: window.screen?.height || 0,
            availWidth: window.screen?.availWidth || 0,
            availHeight: window.screen?.availHeight || 0,
            orientationType: window.screen?.orientation?.type || '',
            orientationAngle: Number(window.screen?.orientation?.angle) || 0
          },
          orientation: window.matchMedia('(orientation: portrait)').matches ? 'portrait' : 'landscape',
          coarsePointer: window.matchMedia('(pointer: coarse)').matches,
          hoverNone: window.matchMedia('(hover: none)').matches,
          maxTouchPoints: navigator.maxTouchPoints || 0,
          userAgentMobile: Boolean(navigator.userAgentData?.mobile),
          documentVisibility: document.visibilityState,
          documentSize: {
            clientWidth: document.documentElement.clientWidth,
            clientHeight: document.documentElement.clientHeight,
            scrollWidth: document.documentElement.scrollWidth,
            scrollHeight: document.documentElement.scrollHeight,
            bodyScrollWidth: document.body?.scrollWidth || 0,
            bodyScrollHeight: document.body?.scrollHeight || 0
          }
        },
        log: document.getElementById('log')?.textContent || '',
        connectionState: typeof pc !== 'undefined' && pc ? pc.connectionState : '',
        iceConnectionState: typeof pc !== 'undefined' && pc ? pc.iceConnectionState : '',
        localTracks: typeof localStream !== 'undefined' && localStream
          ? localStream.getTracks().map(trackProbe)
          : [],
        remoteElements: typeof remoteElements !== 'undefined' ? remoteElements.size : -1,
        remoteElementAttachments: typeof remoteElements !== 'undefined'
          ? Array.from(remoteElements.entries()).map(([name, value]) => ({
              name,
              keys: value && typeof value === 'object' ? Object.keys(value).sort() : [],
              video: videoProbe(mediaElementForValue(value)),
              audioAttached: Boolean((value instanceof HTMLAudioElement) || value?.audio instanceof HTMLAudioElement || value?.audioElement instanceof HTMLAudioElement)
            }))
          : [],
        audioMonitors: typeof audioMonitors !== 'undefined' ? remoteMonitorList.length : -1,
        audioMonitorNames: remoteMonitorList.map(monitor => monitor.name || ''),
        pendingRemotePlayback: typeof pendingRemotePlaybackElements !== 'undefined' ? pendingRemotePlaybackElements.size : -1,
        audiblePendingRemotePlayback: typeof remotePlaybackPendingCount === 'function'
          ? remotePlaybackPendingCount({ audibleOnly: true })
          : (typeof pendingRemotePlaybackElements !== 'undefined' ? pendingRemotePlaybackElements.size : -1),
        mutedPendingRemotePlayback: typeof remotePlaybackPendingCount === 'function'
          ? remotePlaybackPendingCount({ mutedOnly: true })
          : 0,
        remoteAudioPlaybackBlocked: typeof roomAudioPlaybackBlocked === 'function' ? roomAudioPlaybackBlocked() : false,
        audioContextState: typeof audioContext !== 'undefined' && audioContext ? audioContext.state : '',
        playbackGainMonitors: typeof audioMonitors !== 'undefined'
          ? remoteMonitorList.filter(monitor => monitor.playbackGain).length
          : -1,
        audioElementMonitors: typeof audioMonitors !== 'undefined'
          ? remoteMonitorList.filter(monitor => monitor.audio).length
          : -1,
        participantsInRoom: typeof participantsInRoom !== 'undefined' ? participantsInRoom.slice() : [],
        usesCrowdedVideoLimits: typeof useCrowdedVideoLimits === 'function' ? useCrowdedVideoLimits() : null,
        mediaQualityConstrained: typeof mediaQualityConstrained !== 'undefined' ? mediaQualityConstrained : null,
        audioMode: typeof audioSettings !== 'undefined' ? audioSettings.mode : '',
        audioProfile: typeof audioProfileName === 'function' ? audioProfileName() : '',
        audioProcessorState: typeof audioProcessorDiagnosticsSnapshot === 'function' ? audioProcessorDiagnosticsSnapshot() : null,
        voiceProcessor: typeof voiceFocusProcessorType === 'function' ? voiceFocusProcessorType() : '',
        remoteHealth: typeof remoteMediaHealthSnapshot === 'function' ? remoteMediaHealthSnapshot() : null,
        stageMode: document.getElementById('hearthStage')?.dataset.stageMode || '',
        stageParticipant: typeof stageParticipantDisplayName === 'function' ? stageParticipantDisplayName() : '',
        activeSpeaker: typeof activeSpeakerDisplayName === 'function' ? activeSpeakerDisplayName() : '',
        serverActiveSpeaker: typeof serverActiveSpeakerName !== 'undefined' ? serverActiveSpeakerName : '',
        serverActiveSpeakerFresh: typeof authoritativeActiveSpeakerName === 'function' ? Boolean(authoritativeActiveSpeakerName()) : false,
        stageVideo: videoProbe(document.getElementById('activeSpeakerVideo')),
        stageMirrorCanvas: canvasProbe(document.getElementById('activeSpeakerMirrorCanvas')),
        speakerVideo: videoProbe(document.getElementById('activeSpeakerVideo')),
        screenSharing: Boolean(document.getElementById('presentationTile')?.classList.contains('is-screen-sharing')),
        mobileStageDrawerOpen: Boolean(document.getElementById('presentationTile')?.classList.contains('is-mobile-stage-roster-open')),
        stageParticipantsToggle: {
          hidden: Boolean(document.getElementById('stageParticipantsToggle')?.hidden),
          expanded: document.getElementById('stageParticipantsToggle')?.getAttribute('aria-expanded') || ''
        },
        activeScreenShareParticipant: typeof activeScreenShareParticipant !== 'undefined' ? activeScreenShareParticipant : '',
        screenStageVideo: videoProbe(document.getElementById('screenStageVideo')),
        screenShareStripTiles: Array.from(document.querySelectorAll('#presentationTile.is-screen-sharing #videoStack > .video-tile')).map(tile => ({
          participant: tile.dataset.participant || '',
          classes: tile.className,
          rect: rectProbe(tile),
          videos: tile.querySelectorAll('video').length,
          videoDetails: Array.from(tile.querySelectorAll('video')).map(videoProbe),
          renderedVideos: Array.from(tile.querySelectorAll('video')).filter(video => video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA && video.videoWidth > 0 && video.videoHeight > 0).length,
          decodedFrames: Array.from(tile.querySelectorAll('video')).reduce((total, video) => {
            if (typeof video.getVideoPlaybackQuality === 'function') {
              return total + (Number(video.getVideoPlaybackQuality().totalVideoFrames) || 0)
            }
            return total + (Number(video.webkitDecodedFrameCount) || 0)
          }, 0),
          text: tile.textContent.trim().replace(/\\s+/g, ' ').slice(0, 120)
        })),
        boardExpanded: Boolean(document.getElementById('appShell')?.classList.contains('is-board-expanded')),
        boardDockVideos: Array.from(document.querySelectorAll('.board-video-tile')).map(tile => ({
          participant: tile.dataset.participant || '',
          text: tile.textContent.trim().replace(/\\s+/g, ' ').slice(0, 80),
          rect: rectProbe(tile),
          video: videoProbe(tile.querySelector('video'))
        })),
        tiles: Array.from(document.querySelectorAll('.video-tile')).map(tile => ({
          participant: tile.dataset.participant || '',
          classes: tile.className,
          rect: rectProbe(tile),
          videos: tile.querySelectorAll('video').length,
          videoDetails: Array.from(tile.querySelectorAll('video')).map(videoProbe),
          renderedVideos: Array.from(tile.querySelectorAll('video')).filter(video => video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA && video.videoWidth > 0 && video.videoHeight > 0).length,
          decodedFrames: Array.from(tile.querySelectorAll('video')).reduce((total, video) => {
            if (typeof video.getVideoPlaybackQuality === 'function') {
              return total + (Number(video.getVideoPlaybackQuality().totalVideoFrames) || 0)
            }
            return total + (Number(video.webkitDecodedFrameCount) || 0)
          }, 0),
          text: tile.textContent.trim().replace(/\\s+/g, ' ').slice(0, 120)
        })),
        stats
      }
    })()
  `)
}

async function basicPageState(page) {
  return evaluate(page, `
    (() => ({
      readyState: document.readyState,
      log: document.getElementById('log')?.textContent || '',
      accessState: document.getElementById('accessState')?.textContent || '',
      accessHint: document.getElementById('accessHint')?.textContent || '',
      joinDisabled: Boolean(document.getElementById('joinAccess')?.disabled),
      participantValue: document.getElementById('participantName')?.value || '',
      passwordLength: document.getElementById('roomPassword')?.value?.length || 0,
      hasWebSocket: typeof ws !== 'undefined' && Boolean(ws),
      webSocketState: typeof ws !== 'undefined' && ws ? ws.readyState : null,
      hasPeerConnection: typeof pc !== 'undefined' && Boolean(pc),
      peerConnectionState: typeof pc !== 'undefined' && pc ? pc.connectionState : '',
      localStreamTracks: typeof localStream !== 'undefined' && localStream ? localStream.getTracks().map(track => ({ kind: track.kind, readyState: track.readyState, enabled: track.enabled })) : []
    }))()
  `)
}

function validateSnapshots(snapshots, expectedClientCount, options = {}) {
  const failures = []
  const expectedNames = options.expectedNames || config.participants
  for (const snapshot of snapshots) {
    const prefix = options.view ? `${snapshot.name} ${options.view}` : snapshot.name
    const localAudio = snapshot.localTracks.find(track => track.kind === 'audio')
    const localVideo = snapshot.localTracks.find(track => track.kind === 'video')
    if (snapshot.connectionState !== 'connected') {
      failures.push(`${prefix} peer state is ${snapshot.connectionState}`)
    }
    if (!localAudio || localAudio.readyState !== 'live' || !localAudio.enabled) {
      failures.push(`${prefix} local audio is not live/enabled`)
    }
    if (!localVideo || localVideo.readyState !== 'live' || !localVideo.enabled) {
      failures.push(`${prefix} local video is not live/enabled`)
    }
    const outboundAudioBytes = snapshot.stats
      .filter(stat => stat.type === 'outbound-rtp' && stat.kind === 'audio')
      .reduce((total, stat) => total + stat.bytesSent, 0)
    const outboundVideoFrames = snapshot.stats
      .filter(stat => stat.type === 'outbound-rtp' && stat.kind === 'video')
      .reduce((total, stat) => total + Math.max(stat.framesSent || 0, stat.framesEncoded || 0), 0)
    if (localAudio?.readyState === 'live' && localAudio.enabled && outboundAudioBytes <= 0) {
      failures.push(`${prefix} has a live microphone but sent no outbound audio bytes`)
    }
    if (localVideo?.readyState === 'live' && localVideo.enabled && outboundVideoFrames <= 0) {
      failures.push(`${prefix} has a live camera but sent no outbound video frames`)
    }
    if (snapshot.remoteElements < expectedClientCount - 1) {
      failures.push(`${prefix} sees ${snapshot.remoteElements} remote media elements`)
    }
    if (snapshot.audioMonitors !== expectedClientCount - 1) {
      failures.push(`${prefix} has ${snapshot.audioMonitors} remote audio monitors, expected ${expectedClientCount - 1}`)
    }
    if (snapshot.remoteAudioPlaybackBlocked || snapshot.audiblePendingRemotePlayback > 0) {
      failures.push(`${prefix} has blocked remote audio playback (pending=${snapshot.audiblePendingRemotePlayback}, context=${snapshot.audioContextState})`)
    }
    if (expectedClientCount >= 5 && snapshot.usesCrowdedVideoLimits !== true) {
      failures.push(`${prefix} is not using crowded media limits`)
    }
    if (snapshot.playbackGainMonitors > 0) {
      failures.push(`${prefix} still has ${snapshot.playbackGainMonitors} WebAudio playback monitors`)
    }
    if (snapshot.audioElementMonitors < expectedClientCount - 1) {
      failures.push(`${prefix} has ${snapshot.audioElementMonitors} native audio playback monitors`)
    }
    if (!snapshot.remoteHealth) {
      failures.push(`${prefix} has no remote media health snapshot`)
    } else {
      for (const [key, names] of Object.entries({
        missingVideoNames: snapshot.remoteHealth.missingVideoNames || [],
        missingAudioNames: snapshot.remoteHealth.missingAudioNames || [],
        duplicateVideoNames: snapshot.remoteHealth.duplicateVideoNames || [],
        duplicateAudioNames: snapshot.remoteHealth.duplicateAudioNames || [],
        stalledVideoNames: snapshot.remoteHealth.stalledVideoNames || []
      })) {
        if (names.length > 0) {
          failures.push(`${prefix} remote health ${key}=${names.join(',')}`)
        }
      }
      if (snapshot.remoteHealth.placeholderVideoTiles > 0 || snapshot.remoteHealth.placeholderAudioMonitors > 0) {
        failures.push(`${prefix} remote health placeholders video=${snapshot.remoteHealth.placeholderVideoTiles} audio=${snapshot.remoteHealth.placeholderAudioMonitors}`)
      }
      if (snapshot.remoteHealth.audiblePendingRemotePlayback > 0) {
        failures.push(`${prefix} remote health has pending audible playback ${snapshot.remoteHealth.audiblePendingRemotePlayback}`)
      }
      if ((snapshot.remoteHealth.remoteAudioPlaybackPaths?.element || 0) < expectedClientCount - 1) {
        failures.push(`${prefix} remote health is not using native audio playback`)
      }
    }
    const audioProfile = snapshot.audioProfile || (snapshot.audioMode === 'standard' ? 'standard-cleanup' : snapshot.audioMode)
    if (!['standard-cleanup', 'voice-focus'].includes(audioProfile)) {
      failures.push(`${prefix} audio profile is ${audioProfile || snapshot.audioMode}`)
    }
    if (audioProfile === 'voice-focus' && (!snapshot.voiceProcessor || snapshot.voiceProcessor === 'disabled')) {
      failures.push(`${prefix} voice processor is ${snapshot.voiceProcessor}`)
    }
    if (audioProfile === 'standard-cleanup' && snapshot.audioProcessorState?.browserProcessing !== true) {
      failures.push(`${prefix} standard cleanup is not using browser processing`)
    }
    if (options.requireStageView) {
      if (snapshot.stageMode !== 'stage') {
        failures.push(`${prefix} stage mode is ${snapshot.stageMode}`)
      }
      if (!snapshot.stageParticipant) {
        failures.push(`${prefix} has no pinned stage participant`)
      }
      if (!videoProbeRendered(snapshot.stageVideo) && !canvasProbeRendered(snapshot.stageMirrorCanvas)) {
        failures.push(`${prefix} stage surface did not render for ${snapshot.stageParticipant || 'pinned participant'}`)
      }
    }
    if (options.requireBoardDock && !snapshot.usesCrowdedVideoLimits && !usesMobileBoardDockBreakpoint(snapshot)) {
      if (!snapshot.boardExpanded) {
        failures.push(`${prefix} board is not expanded`)
      }
      const renderedDockVideos = snapshot.boardDockVideos.filter(tile => videoProbeRendered(tile.video))
      if (renderedDockVideos.length < expectedClientCount) {
        failures.push(`${prefix} board dock rendered ${renderedDockVideos.length} videos, expected ${expectedClientCount}`)
      }
    }
    for (const name of expectedNames) {
      if (!snapshot.participantsInRoom.includes(name)) {
        failures.push(`${prefix} participant list is missing ${name}`)
      }
      const tileCount = snapshot.tiles.filter(tile => tile.participant === name).length
      if (tileCount !== 1) {
        failures.push(`${prefix} sees ${tileCount} tiles for ${name}`)
      }
      if (name !== snapshot.name) {
        const audioMonitorCount = snapshot.audioMonitorNames.filter(monitorName => monitorName === name).length
        if (audioMonitorCount !== 1) {
          failures.push(`${prefix} has ${audioMonitorCount} audio monitors for ${name}`)
        }
      }
    }
    const placeholderTiles = snapshot.tiles.filter(tile => tile.participant === 'participant').length
    if (placeholderTiles > 0) {
      failures.push(`${prefix} still has ${placeholderTiles} unlabeled participant tiles`)
    }
    const inboundVideoDecoded = snapshot.stats
      .filter(stat => stat.type === 'inbound-rtp' && stat.kind === 'video')
      .reduce((total, stat) => total + stat.framesDecoded, 0)
    const renderedRemoteVideos = snapshot.tiles
      .filter(tile => tile.participant && tile.participant !== snapshot.name)
      .reduce((total, tile) => total + Math.max(tile.renderedVideos || 0, tile.decodedFrames > 0 ? 1 : 0), 0)
    if (expectedClientCount > 1 && inboundVideoDecoded <= 0 && renderedRemoteVideos <= 0) {
      failures.push(`${prefix} has no decoded remote video frames`)
    }
    const candidateRtt = selectedCandidateRtt(snapshot.stats)
    if (candidateRtt > 0.35) {
      failures.push(`${prefix} candidate RTT is ${(candidateRtt * 1000).toFixed(0)}ms`)
    }
    if (snapshot.mediaQualityConstrained) {
      failures.push(`${prefix} media quality was constrained for lag`)
    }
  }
  return failures
}

function usesMobileBoardDockBreakpoint(snapshot) {
  const viewport = snapshot?.viewport || {}
  const visualWidth = Number(viewport.visualViewport?.width) || 0
  const innerWidth = Number(viewport.innerWidth) || 0
  const documentWidth = Number(viewport.documentSize?.clientWidth) || 0
  const width = visualWidth || innerWidth || documentWidth
  return Boolean(width > 0 && width <= 700 && (viewport.coarsePointer || viewport.maxTouchPoints > 0 || viewport.hoverNone))
}

function validateLateJoinSnapshots(snapshots, expectedNames) {
  if (snapshots.length === 0) {
    return []
  }
  return validateSnapshots(snapshots, expectedNames.length, {
    view: 'late join',
    expectedNames
  })
}

function validateScreenShareSnapshots(snapshots, sharerName, expectedNames = config.participants) {
  const failures = []
  for (const snapshot of snapshots) {
    const prefix = `${snapshot.name} screen share`
    if (!snapshot.screenSharing || snapshot.activeScreenShareParticipant !== sharerName) {
      failures.push(`${prefix} is not showing ${sharerName}'s share`)
    }
    if (!videoProbeRendered(snapshot.screenStageVideo)) {
      failures.push(`${prefix} stage video did not render`)
    }
    const visibleStripTiles = snapshot.screenShareStripTiles.filter(tile => !tile.classes.includes('is-sharing-screen'))
    if (visibleStripTiles.length < Math.max(0, expectedNames.length - 1)) {
      failures.push(`${prefix} participant strip has ${visibleStripTiles.length} visible tiles`)
    }
    if (visibleStripTiles.some(tile => tile.participant === sharerName)) {
      failures.push(`${prefix} duplicates the sharer in the participant strip`)
    }
    const renderedStripVideos = visibleStripTiles.filter(tile => tile.renderedVideos > 0 || tile.decodedFrames > 0)
    if (renderedStripVideos.length < visibleStripTiles.length) {
      failures.push(`${prefix} participant strip rendered ${renderedStripVideos.length}/${visibleStripTiles.length} videos`)
    }
  }
  return failures
}

function validateRecordingSnapshots(snapshots, expectedState) {
  const failures = []
  const expectedListening = expectedState === 'on'
  const expectedText = expectedListening ? 'the room is listening' : 'the room is not listening'
  const expectedLabel = expectedListening ? 'Recording' : 'Record off'
  const expectedPressed = expectedListening ? 'true' : 'false'
  for (const snapshot of snapshots) {
    const prefix = `${snapshot.name} recording ${expectedState}`
    if (snapshot.statusText !== expectedText) {
      failures.push(`${prefix} status=${JSON.stringify(snapshot.statusText)}, want ${JSON.stringify(expectedText)}`)
    }
    const listeningClass = String(snapshot.statusClass || '').includes('pill--listening')
    if (listeningClass !== expectedListening) {
      failures.push(`${prefix} listening class=${listeningClass}`)
    }
    if (snapshot.recordLabel !== expectedLabel) {
      failures.push(`${prefix} label=${JSON.stringify(snapshot.recordLabel)}, want ${JSON.stringify(expectedLabel)}`)
    }
    if (snapshot.recordPressed !== expectedPressed) {
      failures.push(`${prefix} aria-pressed=${JSON.stringify(snapshot.recordPressed)}, want ${JSON.stringify(expectedPressed)}`)
    }
  }
  return failures
}

function validateScreenShareStoppedSnapshots(snapshots, sharerName) {
  const failures = []
  for (const snapshot of snapshots) {
    const prefix = `${snapshot.name} after screen share`
    if (snapshot.screenSharing || snapshot.activeScreenShareParticipant) {
      failures.push(`${prefix} still thinks a screen share is active`)
    }
    const sharerTile = snapshot.tiles.find(tile => tile.participant === sharerName)
    if (!sharerTile) {
      failures.push(`${prefix} is missing ${sharerName}'s camera tile after stop`)
      continue
    }
    if (sharerTile.renderedVideos <= 0 && sharerTile.decodedFrames <= 0) {
      failures.push(`${prefix} ${sharerName}'s camera tile did not resume rendering`)
    }
    if (snapshot.remoteHealth?.stalledVideoNames?.includes(sharerName)) {
      failures.push(`${prefix} reports ${sharerName}'s video as stalled`)
    }
  }
  return failures
}

function validateMobileStageDrawerSnapshots(snapshots) {
  const failures = []
  for (const snapshot of snapshots) {
    const prefix = `${snapshot.name} mobile stage drawer`
    if (snapshot.stageMode !== 'stage') {
      failures.push(`${prefix} stage mode is ${snapshot.stageMode}`)
    }
    if (snapshot.stageParticipantsToggle?.hidden) {
      failures.push(`${prefix} people control is hidden`)
    }
    if (!snapshot.mobileStageDrawerOpen || snapshot.stageParticipantsToggle?.expanded !== 'true') {
      failures.push(`${prefix} people drawer did not open`)
    }
  }
  return failures
}

function validatePostLeaveParticipants(postLeave) {
  if (!postLeave || postLeave.skipped) {
    return []
  }
  const failures = []
  if (postLeave.error) {
    failures.push(`post-leave participants fetch failed: ${postLeave.error}`)
  }
  if (postLeave.names.length > 0) {
    failures.push(`post-leave participants still present: ${postLeave.names.join(',')}`)
  }
  return failures
}

function videoProbeRendered(probe) {
  return Boolean(probe
    && !probe.hidden
    && probe.visible
    && probe.hasLiveVideo
    && ((probe.readyState >= 2 && probe.videoWidth > 0 && probe.videoHeight > 0) || probe.frames > 0))
}

function canvasProbeRendered(probe) {
  return Boolean(probe
    && !probe.hidden
    && probe.visible
    && probe.width > 0
    && probe.height > 0)
}

function selectedCandidateRtt(stats) {
  const statsById = new Map(stats.map(stat => [stat.id, stat]))
  const selectedPairIds = stats
    .filter(stat => stat.type === 'transport' && stat.selectedCandidatePairId)
    .map(stat => stat.selectedCandidatePairId)
  const selectedPairs = selectedPairIds
    .map(id => statsById.get(id))
    .filter(Boolean)
  if (selectedPairs.length > 0) {
    return selectedPairs.reduce((max, stat) => Math.max(max, stat.currentRoundTripTime), 0)
  }

  const nominatedPairs = stats
    .filter(stat => stat.type === 'candidate-pair' && stat.nominated && stat.state === 'succeeded')
    .sort((left, right) => right.packetsReceived - left.packetsReceived)
  return nominatedPairs[0]?.currentRoundTripTime || 0
}

async function emitReport(result) {
  if (!config.emitReport) {
    return
  }
  const reportPath = reportOutputPath(config.emitReport)
  const report = {
    startedAt: reportStartedAt,
    generatedAt: new Date().toISOString(),
    config: {
      url: config.url,
      participants: config.participants,
      initialParticipants: initialParticipantNames,
      externalParticipants: config.externalParticipants,
      initialExternalParticipants: initialExternalParticipantNames,
      lateJoin: lateJoinPlan,
      rejoin: config.rejoin,
      separateBrowsers: config.separateBrowsers,
      timeoutMs: config.timeoutMs,
      durationMs: config.durationMs,
      interactive: config.interactive,
      mobileViewport: config.mobileViewport,
      chromePath: config.chromePath,
      passwordLength: String(config.password || '').length
    },
    result
  }
  await mkdir(dirname(reportPath), { recursive: true })
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`)
  log('report-written', reportPath)
}

function reportOutputPath(value) {
  if (value === true) {
    const stamp = reportStartedAt.replace(/[:.]/g, '-')
    return resolve(process.cwd(), `live-media-smoke-report-${stamp}.json`)
  }
  const rawPath = String(value || '').trim()
  if (!rawPath) {
    const stamp = reportStartedAt.replace(/[:.]/g, '-')
    return resolve(process.cwd(), `live-media-smoke-report-${stamp}.json`)
  }
  return isAbsolute(rawPath) ? rawPath : resolve(process.cwd(), rawPath)
}

async function closePages(pages) {
  await Promise.all(pages.map(page => evaluate(page, 'leaveRoom?.(); true').catch(() => {})))
  for (const page of pages) {
    page.client.close()
  }
}

async function closePagesAndCollectParticipants(pages) {
  if (!pages.length) {
    return { skipped: true, reason: 'no automated pages', names: [], snapshot: null, error: '' }
  }
  await Promise.all(pages.map(page => evaluate(page, 'leaveRoom?.(); true').catch(() => {})))
  await sleep(2200)

  let snapshot = null
  let error = ''
  try {
    snapshot = await evaluate(pages[0], `
      (async () => {
        const response = await fetch('/participants', {
          credentials: 'include',
          cache: 'no-store'
        })
        if (!response.ok) {
          return {
            fetchError: response.status + ' ' + response.statusText
          }
        }
        return response.json()
      })()
    `)
    if (snapshot?.fetchError) {
      error = snapshot.fetchError
    }
  } catch (participantsError) {
    error = participantsError?.message || String(participantsError)
  }

  for (const page of pages) {
    page.client.close()
  }
  return {
    skipped: config.externalParticipants.length > 0,
    reason: config.externalParticipants.length > 0 ? 'external participant may still be present' : '',
    names: participantNamesFromSnapshot(snapshot),
    snapshot,
    error
  }
}

function participantNamesFromSnapshot(snapshot) {
  const participants = Array.isArray(snapshot)
    ? snapshot
    : Array.isArray(snapshot?.participants)
      ? snapshot.participants
      : []
  return participants
    .map(participant => {
      if (typeof participant === 'string') {
        return participant
      }
      return participant?.name || participant?.displayName || ''
    })
    .map(name => String(name || '').trim())
    .filter(Boolean)
}

function connectCDP(wsURL) {
  const ws = new WebSocket(wsURL)
  let id = 0
  const pending = new Map()
  const listeners = new Map()
  const rejectPending = error => {
    for (const { reject } of pending.values()) {
      reject(error)
    }
    pending.clear()
  }
  ws.addEventListener('message', event => {
    const message = JSON.parse(event.data)
    if (message.method && listeners.has(message.method)) {
      for (const listener of listeners.get(message.method)) {
        listener(message.params || {})
      }
    }
    if (!message.id || !pending.has(message.id)) {
      return
    }
    const { resolve, reject } = pending.get(message.id)
    pending.delete(message.id)
    if (message.error) {
      reject(new Error(message.error.message || JSON.stringify(message.error)))
    } else {
      resolve(message.result || {})
    }
  })
  ws.addEventListener('close', () => {
    rejectPending(new Error('CDP socket closed'))
  })
  ws.addEventListener('error', () => {
    rejectPending(new Error('CDP socket error'))
  })
  return {
    opened: new Promise((resolve, reject) => {
      ws.addEventListener('open', resolve, { once: true })
      ws.addEventListener('error', reject, { once: true })
    }),
    send(method, params = {}) {
      const requestID = ++id
      ws.send(JSON.stringify({ id: requestID, method, params }))
      return new Promise((resolve, reject) => pending.set(requestID, { resolve, reject }))
    },
    on(method, listener) {
      if (!listeners.has(method)) {
        listeners.set(method, [])
      }
      listeners.get(method).push(listener)
    },
    close() {
      ws.close()
    }
  }
}

async function evaluate(page, expression) {
  const result = await page.client.send('Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true
  })
  if (result.exceptionDetails) {
    throw new Error(result.exceptionDetails.exception?.description || JSON.stringify(result.exceptionDetails))
  }
  return result.result?.value
}

async function waitFor(page, label, expression) {
  const startedAt = Date.now()
  let last
  while (Date.now() - startedAt < config.timeoutMs) {
    try {
      last = await evaluate(page, expression)
      if (last) {
        log('ok', label)
        return last
      }
    } catch (error) {
      last = error.message
    }
    await sleep(500)
  }
  throw new Error(`timed out waiting for ${label}; last=${JSON.stringify(last)}`)
}

async function clickSelector(page, selector) {
  const point = await evaluate(page, `
    (() => {
      const element = document.querySelector(${JSON.stringify(selector)})
      if (!element) {
        throw new Error('missing click target ${selector}')
      }
      const rect = element.getBoundingClientRect()
      if (!rect.width || !rect.height) {
        throw new Error('empty click target ${selector}')
      }
      return {
        x: rect.left + rect.width / 2,
        y: rect.top + rect.height / 2
      }
    })()
  `)
  await page.client.send('Input.dispatchMouseEvent', {
    type: 'mouseMoved',
    x: point.x,
    y: point.y,
    button: 'none'
  })
  await page.client.send('Input.dispatchMouseEvent', {
    type: 'mousePressed',
    x: point.x,
    y: point.y,
    button: 'left',
    clickCount: 1
  })
  await page.client.send('Input.dispatchMouseEvent', {
    type: 'mouseReleased',
    x: point.x,
    y: point.y,
    button: 'left',
    clickCount: 1
  })
}

async function fetchJSON(url, options = {}) {
  const response = await fetch(url, options)
  if (!response.ok) {
    throw new Error(`${response.status} ${url}`)
  }
  return response.json()
}

function log(...parts) {
  console.error(new Date().toISOString(), ...parts)
}

function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms))
}

async function finish(code) {
  if (shuttingDown) {
    return
  }
  shuttingDown = true
  await Promise.all(browsers.map(closeBrowser))
  process.exit(code)
}

async function closeBrowser(browser) {
  if (!browser || browser.closed) {
    return
  }
  browser.closed = true
  if (!browser.chrome.killed) {
    browser.chrome.kill('SIGTERM')
  }
  await rm(browser.userDataDir, { recursive: true, force: true }).catch(() => {})
}
