#!/usr/bin/env node

import { spawn } from 'node:child_process'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const defaults = {
  chromePath: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  password: process.env.ROOM_PASSWORD || 'B0NFIRE!',
  participants: ['Guest 1', 'Guest 2'],
  timeoutMs: 45000,
  url: 'https://thebonfire.xyz'
}

const options = parseArgs(process.argv.slice(2))
const config = {
  ...defaults,
  ...options,
  participants: options.participants || defaults.participants
}

const userDataDir = await mkdtemp(join(tmpdir(), 'meetingassist-live-smoke-'))
const debugPort = await findDebugPort()
const pages = []
const chrome = spawn(config.chromePath, [
  '--headless=new',
  `--remote-debugging-port=${debugPort}`,
  `--user-data-dir=${userDataDir}`,
  '--use-fake-device-for-media-stream',
  '--use-fake-ui-for-media-stream',
  '--autoplay-policy=no-user-gesture-required',
  '--no-first-run',
  '--no-default-browser-check',
  '--disable-features=MediaRouter',
  'about:blank'
], { stdio: ['ignore', 'ignore', 'pipe'] })

chrome.stderr.on('data', data => {
  for (const line of String(data).split('\n')) {
    if (line.includes('DevTools listening') || line.includes('ERROR')) {
      console.error(line)
    }
  }
})

let shuttingDown = false
process.on('SIGINT', () => finish(130))
process.on('SIGTERM', () => finish(143))

try {
  const version = await waitForChrome()
  log('chrome', version.Browser)

  for (const name of config.participants) {
    pages.push(await openPage(name))
  }

  for (const page of pages) {
    await joinRoom(page)
  }

  for (const page of pages) {
    await waitFor(page, `${page.name} remote media`, `
      (() => typeof remoteElements !== 'undefined' && remoteElements.size >= ${pages.length - 1}
        && typeof audioMonitors !== 'undefined' && audioMonitors.size >= ${pages.length - 1})()
    `)
  }

  await sleep(14000)
  const snapshots = []
  for (const page of pages) {
    snapshots.push(await snapshotPage(page))
  }

  const failures = validateSnapshots(snapshots, pages.length)
  console.log(JSON.stringify({ ok: failures.length === 0, failures, snapshots }, null, 2))
  await closePages(pages)
  await finish(failures.length === 0 ? 0 : 1)
} catch (error) {
  if (pages.length) {
    for (const page of pages) {
      try {
        log('failure-snapshot', page.name, JSON.stringify(await basicPageState(page)))
      } catch (snapshotError) {
        log('failure-snapshot-error', page.name, snapshotError.message)
      }
      if (page.events.length) {
        log('page-events', page.name, JSON.stringify(page.events.slice(-12)))
      }
    }
  }
  try {
    const targets = await fetchJSON(`http://127.0.0.1:${debugPort}/json/list`)
    log('open-targets', JSON.stringify(targets.map(target => ({ id: target.id, url: target.url, title: target.title }))))
  } catch {
    // DevTools may already be down.
  }
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
    } else if (arg === '--chrome' && next) {
      parsed.chromePath = next
      index++
    } else if (arg === '--timeout-ms' && next) {
      parsed.timeoutMs = Number(next) || defaults.timeoutMs
      index++
    }
  }
  return parsed
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

async function waitForChrome() {
  for (let attempt = 0; attempt < 120; attempt++) {
    try {
      return await fetchJSON(`http://127.0.0.1:${debugPort}/json/version`)
    } catch {
      await sleep(250)
    }
  }
  throw new Error('Chrome did not expose DevTools')
}

async function openPage(name) {
  const target = await fetchJSON(`http://127.0.0.1:${debugPort}/json/new?about:blank`, { method: 'PUT' })
  const client = connectCDP(target.webSocketDebuggerUrl)
  await client.opened
  await client.send('Runtime.enable')
  await client.send('Page.enable')
  const page = { name, client, events: [] }
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

async function joinRoom(page) {
  await waitFor(page, `${page.name} form`, `
    Boolean(document.getElementById('participantName')
      && document.getElementById('roomPassword')
      && document.getElementById('joinAccess'))
  `)
  await evaluate(page, `
    (() => {
      const participant = document.getElementById('participantName')
      const password = document.getElementById('roomPassword')
      participant.value = ${JSON.stringify(page.name)}
      participant.dispatchEvent(new Event('change', { bubbles: true }))
      password.value = ${JSON.stringify(config.password)}
      password.dispatchEvent(new Event('input', { bubbles: true }))
      document.getElementById('joinAccess').click()
      return true
    })()
  `)
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

async function snapshotPage(page) {
  return evaluate(page, `
    (async () => {
      const stats = typeof pc !== 'undefined' && pc
        ? Array.from((await pc.getStats()).values()).map(stat => ({
            type: stat.type,
            kind: stat.kind || '',
            state: stat.state || '',
            nominated: Boolean(stat.nominated),
            currentRoundTripTime: Number(stat.currentRoundTripTime) || 0,
            jitter: Number(stat.jitter) || 0,
            packetsLost: Number(stat.packetsLost) || 0,
            packetsReceived: Number(stat.packetsReceived) || 0,
            framesDecoded: Number(stat.framesDecoded) || 0,
            framesDropped: Number(stat.framesDropped) || 0,
            framesEncoded: Number(stat.framesEncoded) || 0,
            bytesSent: Number(stat.bytesSent) || 0,
            candidateType: stat.candidateType || '',
            protocol: stat.protocol || '',
            networkType: stat.networkType || ''
          }))
        : []
      return {
        name: ${JSON.stringify(page.name)},
        log: document.getElementById('log')?.textContent || '',
        connectionState: typeof pc !== 'undefined' && pc ? pc.connectionState : '',
        iceConnectionState: typeof pc !== 'undefined' && pc ? pc.iceConnectionState : '',
        localTracks: typeof localStream !== 'undefined' && localStream
          ? localStream.getTracks().map(track => ({
              kind: track.kind,
              enabled: track.enabled,
              readyState: track.readyState,
              settings: track.getSettings?.() || {}
            }))
          : [],
        remoteElements: typeof remoteElements !== 'undefined' ? remoteElements.size : -1,
        audioMonitors: typeof audioMonitors !== 'undefined' ? audioMonitors.size : -1,
        participantsInRoom: typeof participantsInRoom !== 'undefined' ? participantsInRoom.slice() : [],
        mediaQualityConstrained: typeof mediaQualityConstrained !== 'undefined' ? mediaQualityConstrained : null,
        audioMode: typeof audioSettings !== 'undefined' ? audioSettings.mode : '',
        voiceProcessor: typeof voiceFocusProcessorType === 'function' ? voiceFocusProcessorType() : '',
        tiles: Array.from(document.querySelectorAll('.video-tile')).map(tile => ({
          participant: tile.dataset.participant || '',
          classes: tile.className,
          videos: tile.querySelectorAll('video').length,
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

function validateSnapshots(snapshots, expectedClientCount) {
  const failures = []
  for (const snapshot of snapshots) {
    const localAudio = snapshot.localTracks.find(track => track.kind === 'audio')
    const localVideo = snapshot.localTracks.find(track => track.kind === 'video')
    if (snapshot.connectionState !== 'connected') {
      failures.push(`${snapshot.name} peer state is ${snapshot.connectionState}`)
    }
    if (!localAudio || localAudio.readyState !== 'live' || !localAudio.enabled) {
      failures.push(`${snapshot.name} local audio is not live/enabled`)
    }
    if (!localVideo || localVideo.readyState !== 'live' || !localVideo.enabled) {
      failures.push(`${snapshot.name} local video is not live/enabled`)
    }
    if (snapshot.remoteElements < expectedClientCount - 1) {
      failures.push(`${snapshot.name} sees ${snapshot.remoteElements} remote media elements`)
    }
    if (snapshot.audioMonitors < expectedClientCount - 1) {
      failures.push(`${snapshot.name} has ${snapshot.audioMonitors} remote audio monitors`)
    }
    if (snapshot.audioMode !== 'voice-focus') {
      failures.push(`${snapshot.name} audio mode is ${snapshot.audioMode}`)
    }
    if (!['worklet', 'script', 'webaudio'].includes(snapshot.voiceProcessor)) {
      failures.push(`${snapshot.name} voice processor is ${snapshot.voiceProcessor}`)
    }
  }
  return failures
}

async function closePages(pages) {
  await Promise.all(pages.map(page => evaluate(page, 'leaveRoom?.(); true').catch(() => {})))
  for (const page of pages) {
    page.client.close()
  }
}

function connectCDP(wsURL) {
  const ws = new WebSocket(wsURL)
  let id = 0
  const pending = new Map()
  const listeners = new Map()
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
  if (!chrome.killed) {
    chrome.kill('SIGTERM')
  }
  await rm(userDataDir, { recursive: true, force: true }).catch(() => {})
  process.exit(code)
}
