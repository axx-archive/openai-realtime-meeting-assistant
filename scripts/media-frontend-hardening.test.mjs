import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const indexPath = fileURLToPath(new URL('../index.html', import.meta.url))
const source = readFileSync(indexPath, 'utf8')

function sourceBetween(startMarker, endMarker) {
  const start = source.indexOf(startMarker)
  assert.notEqual(start, -1, `missing source marker: ${startMarker}`)
  const end = source.indexOf(endMarker, start + startMarker.length)
  assert.notEqual(end, -1, `missing source marker: ${endMarker}`)
  return source.slice(start, end)
}

function functionSource(name) {
  let start = source.indexOf(`function ${name}(`)
  assert.notEqual(start, -1, `missing function: ${name}`)
  if (source.slice(Math.max(0, start - 6), start) === 'async ') {
    start -= 6
  }
  const parametersOpen = source.indexOf('(', start)
  let parameterDepth = 0
  let parametersClose = -1
  for (let index = parametersOpen; index < source.length; index += 1) {
    if (source[index] === '(') parameterDepth += 1
    if (source[index] === ')') {
      parameterDepth -= 1
      if (parameterDepth === 0) {
        parametersClose = index
        break
      }
    }
  }
  const open = source.indexOf('{', parametersClose)
  assert.notEqual(open, -1, `missing function body: ${name}`)

  let depth = 0
  let quote = ''
  let escaped = false
  let lineComment = false
  let blockComment = false
  for (let index = open; index < source.length; index += 1) {
    const char = source[index]
    const next = source[index + 1]
    if (lineComment) {
      if (char === '\n') lineComment = false
      continue
    }
    if (blockComment) {
      if (char === '*' && next === '/') {
        blockComment = false
        index += 1
      }
      continue
    }
    if (quote) {
      if (escaped) {
        escaped = false
      } else if (char === '\\') {
        escaped = true
      } else if (char === quote) {
        quote = ''
      }
      continue
    }
    if (char === '/' && next === '/') {
      lineComment = true
      index += 1
      continue
    }
    if (char === '/' && next === '*') {
      blockComment = true
      index += 1
      continue
    }
    if (char === "'" || char === '"' || char === '`') {
      quote = char
      continue
    }
    if (char === '{') depth += 1
    if (char === '}') {
      depth -= 1
      if (depth === 0) return source.slice(start, index + 1)
    }
  }
  assert.fail(`unterminated function: ${name}`)
}

function compileFunctions(names, dependencies = {}) {
  const dependencyNames = Object.keys(dependencies)
  const declarations = names.map(functionSource).join('\n')
  const factory = new Function(
    ...dependencyNames,
    `'use strict';\n${declarations}\nreturn { ${names.join(', ')} };`
  )
  return factory(...dependencyNames.map(name => dependencies[name]))
}

function compileStatefulFunctions(names, dependencies, stateAccessors) {
  const dependencyNames = Object.keys(dependencies)
  const declarations = names.map(functionSource).join('\n')
  const factory = new Function(
    ...dependencyNames,
    `'use strict';\n${declarations}\nreturn { ${names.join(', ')}, ${stateAccessors} };`
  )
  return factory(...dependencyNames.map(name => dependencies[name]))
}

function browserReliabilityGate(userAgent, maxTouchPoints = 0) {
  const declarations = [
    sourceBetween('const safariBrowser =', 'const isIOSDevice ='),
    sourceBetween('const isIOSDevice =', 'const isAndroidDevice ='),
    sourceBetween('const isMobileDevice =', '// Video looks are deliberately Chrome-desktop-only.'),
    sourceBetween('const reliableVideoLookBrowser =', 'const videoLooksDisabledForReliability ='),
    sourceBetween('const videoLooksDisabledForReliability =', '// The canvas mirror is an iOS/WebKit workaround')
  ].join('\n')
  return new Function(
    'forcedSafariMediaPath',
    'navigator',
    `'use strict';\n${declarations}\nreturn { safariBrowser, isIOSDevice, isMobileDevice, reliableVideoLookBrowser, videoLooksDisabledForReliability };`
  )(false, { userAgent, maxTouchPoints })
}

function videoLookControls(disabled) {
  const chips = ['none', 'studio'].map(look => ({
    dataset: { look },
    classList: { toggle() {} },
    setAttribute() {},
    disabled: null
  }))
  const blur = { checked: false, disabled: null }
  const brightness = { value: '', disabled: null }
  const warmth = { value: '', disabled: null }
  const intensity = { value: '', disabled: null }
  const { renderVideoLookSettings } = compileFunctions(['renderVideoLookSettings'], {
    videoLookSettingsValue: () => ({ look: 'studio', intensity: 1 }),
    videoLookChips: chips,
    videoLooksDisabledForReliability: disabled,
    greenRoomBlurEl: blur,
    videoLookPresetUniforms: () => ({ uBrightness: 0, uTemperature: 0 }),
    videoLookBrightness: brightness,
    videoLookWarmth: warmth,
    videoLookIntensityInput: intensity,
    renderVideoLookStatus() {},
    videoLookSavedHint: null,
    audioSettingsHasSavedRecord: () => false
  })
  renderVideoLookSettings()
  return { chips, blur, brightness, warmth, intensity }
}

test('video looks are selectable only on Chrome desktop', () => {
  const agents = {
    chromeDesktop: { userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36' },
    safariDesktop: { userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Safari/605.1.15' },
    iPhoneSafari: { userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 Safari/604.1' },
    androidChrome: { userAgent: 'Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36' },
    edgeDesktop: { userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0' },
    firefoxDesktop: { userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:141.0) Gecko/20100101 Firefox/141.0' },
    iPadChrome: { userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/140.0.0.0 Safari/604.1', maxTouchPoints: 5 },
    iPadFirefox: { userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/141.0 Safari/605.1.15', maxTouchPoints: 5 }
  }

  assert.equal(browserReliabilityGate(agents.chromeDesktop.userAgent).reliableVideoLookBrowser, true)
  for (const [name, agent] of Object.entries(agents).filter(([name]) => name !== 'chromeDesktop')) {
    assert.equal(browserReliabilityGate(agent.userAgent, agent.maxTouchPoints || 0).videoLooksDisabledForReliability, true, name)
  }
  assert.equal(browserReliabilityGate(agents.iPadChrome.userAgent, 5).isMobileDevice, true)
  assert.equal(browserReliabilityGate(agents.iPadFirefox.userAgent, 5).isMobileDevice, true)

  const chromeControls = videoLookControls(false)
  assert.ok(chromeControls.chips.every(chip => chip.disabled === false))
  assert.equal(chromeControls.blur.disabled, false)
  assert.equal(chromeControls.brightness.disabled, false)
  assert.equal(chromeControls.warmth.disabled, false)
  assert.equal(chromeControls.intensity.disabled, false)

  const fallbackControls = videoLookControls(true)
  assert.ok(fallbackControls.chips.every(chip => chip.disabled === true))
  assert.equal(fallbackControls.blur.disabled, true)
  assert.equal(fallbackControls.brightness.disabled, true)
  assert.equal(fallbackControls.warmth.disabled, true)
  assert.equal(fallbackControls.intensity.disabled, true)
  assert.match(functionSource('renderGreenRoom'), /wrap\.hidden = videoLooksDisabledForReliability/)
})

test('local mirror reveals the canvas only after a commit and preserves last-good pixels', () => {
  const state = {
    backCanvas: { width: 0, height: 0, getContext: () => backContext },
    hasFrame: false,
    commits: 0,
    failures: 0
  }
  const canvasClasses = new Map()
  const localVideo = {
    videoWidth: 640,
    videoHeight: 480,
    classList: { toggle: (name, enabled) => canvasClasses.set(name, enabled) }
  }
  let rect = { width: 200, height: 100 }
  let backDrawFails = true
  let frontDrawFails = false
  let frontDraws = 0
  const backContext = {
    save() {}, restore() {}, setTransform() {}, clearRect() {}, translate() {}, scale() {},
    drawImage() { if (backDrawFails) throw new Error('transient WebKit draw failure') }
  }
  const frontContext = {
    globalCompositeOperation: 'source-over',
    setTransform() {},
    drawImage() {
      if (frontDrawFails) throw new Error('visible WebKit context lost after resize')
      frontDraws += 1
    }
  }
  const canvas = {
    width: 320,
    height: 180,
    hidden: true,
    dataset: {},
    parentElement: { getBoundingClientRect: () => rect },
    getContext: () => frontContext
  }
  const setLocalMirrorCanvasReady = (_canvas, ready) => {
    state.hasFrame = Boolean(ready)
    canvas.dataset.mirrorFrameReady = ready ? 'true' : 'false'
    canvas.hidden = !ready
  }

  const { syncLocalMirrorPreview, drawLocalMirrorPreviewToCanvas } = compileFunctions([
    'syncLocalMirrorPreview',
    'drawLocalMirrorPreviewToCanvas'
  ], {
    localMirrorCanvases: () => [{ canvas, enabled: true }],
    localMirrorCanvasState: () => state,
    localVideo,
    document: { visibilityState: 'visible' },
    localMirrorFrame: 0,
    window: { devicePixelRatio: 2, requestAnimationFrame: () => 1 },
    drawLocalMirrorPreviewFrame() {},
    stopLocalMirrorPreview() {},
    setLocalMirrorCanvasReady
  })

  syncLocalMirrorPreview()
  assert.equal(canvas.hidden, true)
  assert.equal(canvasClasses.get('local-mirror-source'), false)

  drawLocalMirrorPreviewToCanvas(canvas)
  assert.equal(state.hasFrame, false)
  assert.equal(state.failures, 1)
  assert.deepEqual([canvas.width, canvas.height], [320, 180])
  assert.equal(frontDraws, 0)

  backDrawFails = false
  drawLocalMirrorPreviewToCanvas(canvas)
  syncLocalMirrorPreview()
  assert.equal(state.hasFrame, true)
  assert.equal(state.commits, 1)
  assert.equal(canvas.hidden, false)
  assert.equal(canvasClasses.get('local-mirror-source'), true)
  assert.deepEqual([canvas.width, canvas.height], [400, 200])
  assert.equal(frontDraws, 1)

  rect = { width: 100, height: 200 }
  backDrawFails = true
  drawLocalMirrorPreviewToCanvas(canvas)
  assert.equal(state.hasFrame, true)
  assert.equal(state.failures, 2)
  assert.equal(canvas.hidden, false)
  assert.deepEqual([canvas.width, canvas.height], [400, 200])
  assert.equal(frontDraws, 1)

  backDrawFails = false
  frontDrawFails = true
  drawLocalMirrorPreviewToCanvas(canvas)
  syncLocalMirrorPreview()
  assert.equal(state.hasFrame, false)
  assert.equal(state.failures, 3)
  assert.equal(canvas.hidden, true)
  assert.equal(canvasClasses.get('local-mirror-source'), false)
  assert.deepEqual([canvas.width, canvas.height], [200, 400])
  assert.equal(frontDraws, 1)
  assert.match(sourceBetween('#localVideo {', '.uses-local-mirror-canvas .hearth-speaker'), /transform: scaleX\(-1\)/)
  assert.match(sourceBetween('.uses-local-mirror-canvas #localVideo.local-mirror-source {', '.video-label {'), /opacity: 0/)
  assert.match(functionSource('drawLocalMirrorPreviewToCanvas'), /canvas\.parentElement\?\.getBoundingClientRect/)
})

test('explicit camera-off intent blocks ended-track and visibility reacquisition', async () => {
  let recoveryRequests = 0
  let recoveryResets = 0
  const endedTrack = { kind: 'video', readyState: 'ended', enabled: true }
  const { setLocalCameraOff, restoreVideoAfterVisibility } = compileFunctions([
    'setLocalCameraOff',
    'restoreVideoAfterVisibility'
  ], {
    localStream: {},
    isCameraOff: false,
    localCameraRequested: true,
    localVideoTrack: () => endedTrack,
    liveTrack: track => Boolean(track && track.readyState !== 'ended'),
    resetLocalVideoRecovery: () => { recoveryResets += 1 },
    localVideoRecoveryPromise: null,
    requestLocalVideoRecovery: async () => { recoveryRequests += 1; return true },
    updateCameraButton() {},
    publishParticipantMediaState() {},
    setLog() {},
    updateBoardDockControls() {}
  })

  setLocalCameraOff(true, { announce: true })
  assert.equal(recoveryResets, 1)
  assert.equal(await restoreVideoAfterVisibility(), false)
  assert.equal(recoveryRequests, 0)
})

function compileLocalVideoRecovery(overrides = {}) {
  const endedTrack = { id: 'ended-camera', kind: 'video', readyState: 'ended' }
  let currentTrack = endedTrack
  const sessionStream = {
    getVideoTracks: () => currentTrack ? [currentTrack] : []
  }
  const ws = { readyState: 1 }
  const defaults = {
    localVideoRecoveryPromise: null,
    localCameraRequested: true,
    isReplacingLocalVideo: false,
    localStream: sessionStream,
    ws,
    WebSocket: { OPEN: 1 },
    liveTrack: track => Boolean(track && track.readyState !== 'ended'),
    localVideoTrack: () => currentTrack,
    localVideoRecoveryAttempts: 0,
    maxLocalVideoRecoveryAttempts: 3,
    mediaConstraints: { video: { tier: 'full' } },
    relaxedVideoConstraints: { tier: 'relaxed' },
    window: { setTimeout: callback => { queueMicrotask(callback); return 1 } },
    navigator: { mediaDevices: { getUserMedia: async () => { throw new Error('not configured') } } },
    applyVideoLookToTracks: async tracks => tracks,
    localVideoLookRawTrack: null,
    teardownVideoLookPipeline() {},
    replaceLocalVideoTrack: async track => { currentTrack = track; return true },
    setLog() {},
    localVideoRecoveryStableTimer: null,
    localVideoRecoveryStableMs: 5000,
    resetLocalVideoRecovery() {},
    console: { warn() {} },
    isCameraOff: true,
    updateCameraButton() {},
    publishParticipantMediaState() {},
    showToast() {},
    reportClientMediaError() {}
  }
  const dependencies = { ...defaults, ...overrides }
  return {
    ...compileFunctions(['requestLocalVideoRecovery'], dependencies),
    sessionStream,
    ws,
    setCurrentTrack: track => { currentTrack = track }
  }
}

test('ended camera recovery is bounded to the three decreasing constraint tiers', async () => {
  const attempts = []
  const recoveredTrack = { id: 'recovered-camera', kind: 'video', readyState: 'live', stop() {} }
  const recoveredStream = {
    getVideoTracks: () => [recoveredTrack],
    getTracks: () => [recoveredTrack]
  }
  const recovery = compileLocalVideoRecovery({
    navigator: {
      mediaDevices: {
        async getUserMedia(constraints) {
          attempts.push(constraints)
          if (attempts.length < 3) {
            const error = new Error('capture tier unavailable')
            error.name = attempts.length === 1 ? 'OverconstrainedError' : 'NotReadableError'
            throw error
          }
          return recoveredStream
        }
      }
    }
  })

  assert.equal(await recovery.requestLocalVideoRecovery('camera interrupted'), true)
  assert.deepEqual(attempts, [
    { video: { tier: 'full' }, audio: false },
    { video: { tier: 'relaxed' }, audio: false },
    { video: true, audio: false }
  ])
})

test('a camera track arriving after the room is left is stopped instead of attached', async () => {
  let beginCapture
  const captureStarted = new Promise(resolve => { beginCapture = resolve })
  let resolveCapture
  const lateCapture = new Promise(resolve => { resolveCapture = resolve })
  let stopCalls = 0
  let replaceCalls = 0
  const lateTrack = {
    id: 'late-camera',
    kind: 'video',
    readyState: 'live',
    stop: () => { stopCalls += 1 }
  }
  const lateStream = {
    getVideoTracks: () => [lateTrack],
    getTracks: () => [lateTrack]
  }
  const recovery = compileLocalVideoRecovery({
    navigator: {
      mediaDevices: {
        getUserMedia() {
          beginCapture()
          return lateCapture
        }
      }
    },
    replaceLocalVideoTrack: async () => { replaceCalls += 1; return true }
  })

  const result = recovery.requestLocalVideoRecovery('camera interrupted')
  await captureStarted
  recovery.ws.readyState = 3
  resolveCapture(lateStream)

  assert.equal(await result, false)
  assert.equal(stopCalls, 1)
  assert.equal(replaceCalls, 0)
})

test('refreshing the parked camera never steals an active screen-share sender', async () => {
  let localTracks
  let oldTrackStops = 0
  const oldCamera = { id: 'old-camera', kind: 'video', readyState: 'live', stop: () => { oldTrackStops += 1 } }
  const newCamera = { id: 'new-camera', kind: 'video', readyState: 'live', enabled: false }
  const screenTrack = { id: 'screen', kind: 'video', readyState: 'live' }
  localTracks = [oldCamera]
  const localStream = {
    getVideoTracks: () => [...localTracks],
    removeTrack: track => { localTracks = localTracks.filter(candidate => candidate !== track) },
    addTrack: track => { localTracks.push(track) }
  }
  const senderReplacements = []
  const sender = {
    track: screenTrack,
    replaceTrack: async track => { senderReplacements.push(track); sender.track = track }
  }
  const { replaceLocalVideoTrack } = compileFunctions(['replaceLocalVideoTrack'], {
    localStream,
    isCameraOff: false,
    localVideoTrack: () => localTracks[0] || null,
    pc: { getSenders: () => [sender] },
    setTrackContentHint() {},
    cameraContentHint: 'motion',
    isReplacingLocalVideo: false,
    activeScreenShareTrack: () => screenTrack,
    configureOutboundSender() {},
    setVideoElementStream() {},
    localVideo: {},
    wireLocalVideoTrack() {},
    updateCameraButton() {},
    publishParticipantMediaState() {},
    updateHearthParticipants() {}
  })

  assert.equal(await replaceLocalVideoTrack(newCamera, { cameraOff: false }), true)
  assert.deepEqual(senderReplacements, [])
  assert.equal(sender.track, screenTrack)
  assert.deepEqual(localTracks, [newCamera])
  assert.equal(oldTrackStops, 1)
  assert.match(functionSource('outboundVideoTrack'), /activeScreenShareTrack\(\) \|\| localVideoTrack\(\)/)
})

test('iOS stage ownership transfers one decoder in detach-before-attach order', () => {
  const calls = []
  const track = { id: 'remote-video', kind: 'video', readyState: 'live' }
  const stream = { getVideoTracks: () => [track] }
  const tileA = { classList: { add: name => calls.push(`tile-a:add:${name}`), remove: name => calls.push(`tile-a:remove:${name}`) } }
  const tileB = { classList: { add: name => calls.push(`tile-b:add:${name}`), remove: name => calls.push(`tile-b:remove:${name}`) } }
  const canonicalA = { id: 'canonical-a', srcObject: stream, closest: () => tileA }
  const canonicalB = { id: 'canonical-b', srcObject: stream, closest: () => tileB }
  let canonical = canonicalA
  const screenStageVideo = { id: 'stage', dataset: {}, srcObject: null }
  const setVideoElementStream = (video, nextStream) => {
    calls.push(`stream:${video.id}:${nextStream ? 'live' : 'none'}`)
    video.srcObject = nextStream || null
  }

  const ownership = compileFunctions([
    'iosScreenStageVideoOwner',
    'iosScreenStageOwnsParticipant',
    'releaseIOSScreenStageVideoOwner',
    'claimIOSScreenStageVideoOwner'
  ], {
    screenStageVideo,
    isIOSDevice: true,
    currentParticipantName: 'Tom',
    setVideoElementStream,
    primaryVideoElementForParticipant: () => canonical,
    participantStream: () => stream,
    participantMediaState: () => ({ cameraOff: false }),
    playRemoteMedia: video => calls.push(`play:${video.id}`),
    syncRemoteParticipantVideoMappings: () => { calls.push('sync-maps'); return true },
    liveTrack: candidate => candidate?.readyState !== 'ended',
    promoteParticipantAudioToVideo: name => calls.push(`promote:${name}`),
    videoElementHasLiveVideoTrack: video => Boolean(video?.srcObject?.getVideoTracks?.().some(candidate => candidate.readyState !== 'ended')),
    demoteRemotePlaybackElementFromVideo: (video, name) => calls.push(`demote:${video.id}:${name}`)
  })

  assert.equal(ownership.claimIOSScreenStageVideoOwner('Caitlyn', stream), true)
  assert.deepEqual(calls.slice(0, 4), [
    'demote:canonical-a:Caitlyn',
    'stream:canonical-a:none',
    'tile-a:add:is-stage-video-owned',
    'stream:stage:live'
  ])
  assert.equal(screenStageVideo.dataset.participantVideoOwner, 'Caitlyn')

  calls.length = 0
  canonical = canonicalB
  assert.equal(ownership.claimIOSScreenStageVideoOwner('Caitlyn', stream), true)
  assert.deepEqual(calls, [
    'demote:canonical-b:Caitlyn',
    'stream:canonical-b:none',
    'tile-b:add:is-stage-video-owned',
    'stream:stage:live'
  ])
  assert.equal(screenStageVideo.dataset.participantVideoOwner, 'Caitlyn')

  calls.length = 0
  assert.equal(ownership.releaseIOSScreenStageVideoOwner(), true)
  assert.deepEqual(calls, [
    'stream:stage:none',
    'tile-b:remove:is-stage-video-owned',
    'stream:canonical-b:live',
    'play:canonical-b',
    'sync-maps',
    'promote:Caitlyn'
  ])
  assert.equal(screenStageVideo.dataset.participantVideoOwner, undefined)
})

test('stage-owned iOS video remains visible to health, freeze, and track mapping', () => {
  const track = { id: 'stage-track', kind: 'video', readyState: 'live' }
  const stream = { getVideoTracks: () => [track] }
  const screenStageVideo = {
    dataset: { participantVideoOwner: 'Caitlyn' },
    srcObject: stream,
    readyState: 2,
    videoWidth: 1280,
    videoHeight: 720
  }
  const tile = {
    dataset: { participant: 'Caitlyn' },
    classList: { contains: name => name === 'is-stage-video-owned' },
    querySelector: () => null
  }
  const remoteVideoFreezeStates = new Map([[track.id, { lastAdvanceAt: 990 }]])
  const frozenRemoteVideoTrackIds = new Set()
  const health = compileFunctions([
    'iosScreenStageVideoOwner',
    'iosScreenStageOwnsParticipant',
    'remoteTileHasLiveVideo',
    'remoteTileHasRenderedVideo',
    'remoteVideoCongestionCensus',
    'remoteTileShowingTrack'
  ], {
    screenStageVideo,
    isIOSDevice: true,
    remoteTileName: candidate => candidate?.dataset?.participant || '',
    videoElementHasLiveVideoTrack: video => Boolean(video?.srcObject?.getVideoTracks?.().some(candidate => candidate.readyState !== 'ended')),
    liveTrack: candidate => candidate?.readyState !== 'ended',
    frozenRemoteVideoTrackIds,
    HTMLMediaElement: { HAVE_CURRENT_DATA: 2 },
    remoteTiles: () => [tile],
    remoteVideoFreezeStates,
    participantMediaStates: new Map(),
    participantMediaState: () => ({ cameraOff: false }),
    remoteVideoFreezeStallMs: 2000
  })

  assert.equal(health.remoteTileHasLiveVideo(tile), true)
  assert.equal(health.remoteTileHasRenderedVideo(tile), true)
  assert.equal(health.remoteTileShowingTrack(track), tile)
  assert.deepEqual(health.remoteVideoCongestionCensus(1000), {
    sickTiles: 0,
    advancingTiles: 1,
    trackedTiles: 1
  })

  frozenRemoteVideoTrackIds.add(track.id)
  assert.equal(health.remoteTileHasRenderedVideo(tile), false)
  assert.deepEqual(health.remoteVideoCongestionCensus(1000), {
    sickTiles: 1,
    advancingTiles: 0,
    trackedTiles: 1
  })
})

test('screen-share stop schedules primary video repair for narrow layouts', () => {
  const repairs = []
  const screenStageVideo = {
    closest: () => ({ classList: { remove() {} } })
  }
  const { renderScreenStage } = compileFunctions(['renderScreenStage'], {
    screenStageVideo,
    activeScreenShareParticipant: '',
    presentationTile: { classList: { remove() {} } },
    releaseIOSScreenStageVideoOwner: () => { repairs.push('release-stage'); return true },
    setVideoElementStream: (video, stream) => repairs.push(`stream:${video === screenStageVideo ? 'stage' : 'speaker'}:${stream ? 'live' : 'none'}`),
    screenStageLabel: { textContent: '' },
    screenSpeakerVideo: {},
    screenSpeakerLabel: { textContent: '' },
    updateStageControls() {},
    updateBoardDockControls() {},
    scheduleAuxiliaryVideoPlaybackRepair: (reason, delay) => repairs.push(`aux:${reason}:${delay}`),
    schedulePrimaryVideoPlaybackRepair: (reason, delay) => repairs.push(`primary:${reason}:${delay}`)
  })

  renderScreenStage()
  assert.ok(repairs.includes('primary:screen stage cleared:140'))
  assert.ok(repairs.indexOf('release-stage') < repairs.indexOf('primary:screen stage cleared:140'))
  assert.match(functionSource('handleScreenShareStopped'), /renderScreenStage\(\)/)
})

function remoteICEHarness(remoteDescription = null) {
  const queued = []
  const added = []
  const pc = {
    remoteDescription,
    addIceCandidate: async candidate => { added.push(candidate) }
  }
  const functions = compileFunctions([
    'remoteDescriptionICEUfrags',
    'remoteIceCandidateUsernameFragment',
    'remoteIceCandidateMatchesDescription',
    'queueRemoteIceCandidate',
    'addRemoteIceCandidate',
    'flushQueuedRemoteCandidates'
  ], {
    pc,
    queuedRemoteCandidates: queued,
    console: { info() {}, warn() {} }
  })
  return { ...functions, pc, queued, added }
}

test('remote ICE queues a future generation while admitting the current generation', async () => {
  const oldDescription = {
    type: 'offer',
    sdp: 'v=0\r\na=ice-ufrag:old-generation\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=ice-ufrag:old-generation\r\n'
  }
  const harness = remoteICEHarness(oldDescription)
  const future = { candidate: 'candidate:future', sdpMid: '0', usernameFragment: 'new-generation' }
  const current = { candidate: 'candidate:current', sdpMid: '0', usernameFragment: 'old-generation' }
  const pionFuture = {
    candidate: 'candidate:842163049 1 udp 1677734910 192.0.2.10 54400 typ srflx raddr 0.0.0.0 rport 0 ufrag new-generation',
    sdpMid: '0',
    sdpMLineIndex: 0
  }
  const pionCurrent = {
    candidate: 'candidate:842163050 1 udp 1677734910 192.0.2.11 54401 typ srflx raddr 0.0.0.0 rport 0 ufrag old-generation',
    sdpMid: '0',
    sdpMLineIndex: 0,
    usernameFragment: null
  }

  await harness.addRemoteIceCandidate(future)
  assert.equal(harness.added.length, 0)
  assert.equal(harness.queued.length, 1)
  assert.equal(harness.queued[0].candidate, future)

  await harness.addRemoteIceCandidate(current)
  assert.deepEqual(harness.added, [current])
  assert.equal(harness.queued.length, 1)

  await harness.addRemoteIceCandidate(pionFuture)
  assert.equal(harness.queued.length, 2)
  assert.equal(harness.queued[1].candidate, pionFuture)
  await harness.addRemoteIceCandidate(pionCurrent)
  assert.deepEqual(harness.added, [current, pionCurrent])
})

test('remote ICE pre-offer candidates flush, and a new offer rejects stale ufrags', async () => {
  const preOffer = remoteICEHarness(null)
  const blankUfrag = { candidate: 'candidate:pre-offer', sdpMid: '0' }
  await preOffer.addRemoteIceCandidate(blankUfrag)
  assert.equal(preOffer.queued.length, 1)
  preOffer.pc.remoteDescription = { type: 'offer', sdp: 'v=0\r\na=ice-ufrag:new-generation\r\n' }
  await preOffer.flushQueuedRemoteCandidates()
  assert.deepEqual(preOffer.added, [blankUfrag])

  const restart = remoteICEHarness({ type: 'offer', sdp: 'v=0\r\na=ice-ufrag:new-generation\r\n' })
  const fresh = { candidate: 'candidate:fresh', sdpMid: '0', usernameFragment: 'new-generation' }
  const stale = { candidate: 'candidate:stale', sdpMid: '0', usernameFragment: 'old-generation' }
  restart.queueRemoteIceCandidate(stale)
  restart.queueRemoteIceCandidate(fresh)
  await restart.flushQueuedRemoteCandidates()
  assert.deepEqual(restart.added, [fresh])
})

test('remote ICE queue deduplicates candidate identity and retains only the newest 64', () => {
  const harness = remoteICEHarness(null)
  const duplicate = { candidate: 'candidate:duplicate', sdpMid: '0', sdpMLineIndex: 0, usernameFragment: 'next' }
  assert.equal(harness.queueRemoteIceCandidate(duplicate), true)
  assert.equal(harness.queueRemoteIceCandidate({ ...duplicate }), false)
  assert.equal(harness.queued.length, 1)

  const bounded = remoteICEHarness(null)
  for (let index = 0; index < 70; index += 1) {
    assert.equal(bounded.queueRemoteIceCandidate({
      candidate: `candidate:${index}`,
      sdpMid: '0',
      sdpMLineIndex: 0,
      usernameFragment: 'next'
    }), true)
  }
  assert.equal(bounded.queued.length, 64)
  assert.equal(bounded.queued[0].candidate.candidate, 'candidate:6')
  assert.equal(bounded.queued.at(-1).candidate.candidate, 'candidate:69')
})

test('a signal callback queued on an old socket no-ops after chain reset and replacement', async () => {
  let releaseOldChain
  const oldGate = new Promise(resolve => { releaseOldChain = resolve })
  const sockets = []
  class FakeWebSocket {
    constructor(url) {
      this.url = url
      this.readyState = 1
      sockets.push(this)
    }
    send() {}
  }
  let handled = 0
  const oldPeer = { id: 'old-peer' }
  const replacementPeer = { id: 'replacement-peer' }
  const replacementSocket = { id: 'replacement-socket', readyState: 1, send() {} }
  const harness = compileStatefulFunctions(['openRoomWebSocket'], {
    window: {
      location: { protocol: 'http:', host: 'localhost:3191' },
      clearInterval() {},
      setInterval: () => 1
    },
    WebSocket: FakeWebSocket,
    activeJoin: { roomId: 'office', passcode: '' },
    guestMode: false,
    ws: null,
    signalChain: oldGate,
    pc: oldPeer,
    accessState: { textContent: '' },
    accessHint: { textContent: '' },
    currentParticipantName: 'Tom',
    ensureEndpointId: () => 'endpoint-test',
    console: { warn() {} },
    setLog() {},
    handleSignal: async () => { handled += 1 },
    handleRoomWebSocketClose() {},
    refreshAuthState() {}
  }, [
    'resetSignalChain: () => { signalChain = Promise.resolve() }',
    'replaceSignalContext: (socket, peer) => { ws = socket; pc = peer }',
    'currentSignalChain: () => signalChain'
  ].join(', '))

  harness.openRoomWebSocket()
  const oldSocket = sockets[0]
  oldSocket.onmessage({ data: JSON.stringify({ event: 'candidate', data: '{}' }) })
  const queuedOnOldChain = harness.currentSignalChain()
  harness.resetSignalChain()
  harness.replaceSignalContext(replacementSocket, replacementPeer)
  releaseOldChain()
  await queuedOnOldChain

  assert.equal(handled, 0)
})

function signalOfferHarness({ peer, socket, waitForStableSignaling, flushQueuedRemoteCandidates, attachLocalTracksToOffer } = {}) {
  const oldPeer = peer || {
    signalingState: 'stable',
    async setRemoteDescription() {},
    async createAnswer() { return { type: 'answer', sdp: 'answer' } },
    async setLocalDescription() {}
  }
  const oldSocket = socket || { readyState: 1, send() {} }
  const replacementPeer = { id: 'replacement-peer' }
  const replacementSocket = { id: 'replacement-socket', readyState: 1, send() { assert.fail('replacement socket must not send an old answer') } }
  const dependencies = {
    ws: oldSocket,
    pc: oldPeer,
    replacementPeer,
    replacementSocket,
    WebSocket: { OPEN: 1 },
    addAssistantMessage() {},
    waitForStableSignaling: waitForStableSignaling || (async () => true),
    flushQueuedRemoteCandidates: flushQueuedRemoteCandidates || (async () => {}),
    attachLocalTracksToOffer: attachLocalTracksToOffer || (async () => {}),
    rollbackRemoteOffer: async () => false,
    configureOutboundSenders: async () => {},
    addRemoteIceCandidate: async () => {},
    handleKanbanMessage() {},
    console: { warn() {} }
  }
  const harness = compileStatefulFunctions(['handleSignal'], dependencies, [
    'replaceSignalContext: () => { ws = replacementSocket; pc = replacementPeer }'
  ].join(', '))
  return { ...harness, oldPeer, oldSocket, replacementPeer, replacementSocket }
}

const offerMessage = {
  event: 'offer',
  data: JSON.stringify({ type: 'offer', sdp: 'v=0\r\n' }),
  offerId: 'offer-test',
  revision: 1
}

test('offer handling stops if the socket or peer changes while signaling waits', async () => {
  const timers = []
  const peer = {
    signalingState: 'have-local-offer',
    remoteDescriptions: 0,
    async setRemoteDescription() { this.remoteDescriptions += 1 }
  }
  const socket = { readyState: 1, send() { assert.fail('stale offer must not send') } }
  const replacementPeer = { id: 'replacement-peer' }
  const replacementSocket = { id: 'replacement-socket', readyState: 1 }
  const harness = compileStatefulFunctions(['waitForStableSignaling', 'handleSignal'], {
    ws: socket,
    pc: peer,
    replacementPeer,
    replacementSocket,
    WebSocket: { OPEN: 1 },
    window: { setTimeout: resolve => { timers.push(resolve); return timers.length } },
    performance: { now: () => 0 },
    rollbackRemoteOffer: async () => false,
    addAssistantMessage() {},
    flushQueuedRemoteCandidates: async () => {},
    attachLocalTracksToOffer: async () => {},
    configureOutboundSenders: async () => {},
    addRemoteIceCandidate: async () => {},
    handleKanbanMessage() {},
    console: { warn() {} }
  }, 'replaceSignalContext: () => { ws = replacementSocket; pc = replacementPeer }')

  const handling = harness.handleSignal(offerMessage, { socket, sessionPeer: peer })
  while (!timers.length) await Promise.resolve()
  harness.replaceSignalContext()
  timers.shift()()
  await handling
  assert.equal(peer.remoteDescriptions, 0)
})

test('offer handling fences every awaited peer mutation before continuing or sending', async () => {
  const stages = [
    {
      name: 'setRemoteDescription',
      peerFactory(deferred) {
        return {
          signalingState: 'stable',
          remoteCalls: 0,
          createCalls: 0,
          localCalls: 0,
          async setRemoteDescription() { this.remoteCalls += 1; return deferred.promise },
          async createAnswer() { this.createCalls += 1; return { type: 'answer', sdp: 'answer' } },
          async setLocalDescription() { this.localCalls += 1 }
        }
      },
      expected: { remoteCalls: 1, createCalls: 0, localCalls: 0 }
    },
    {
      name: 'createAnswer',
      peerFactory(deferred) {
        return {
          signalingState: 'stable',
          remoteCalls: 0,
          createCalls: 0,
          localCalls: 0,
          async setRemoteDescription() { this.remoteCalls += 1 },
          async createAnswer() { this.createCalls += 1; return deferred.promise },
          async setLocalDescription() { this.localCalls += 1 }
        }
      },
      expected: { remoteCalls: 1, createCalls: 1, localCalls: 0 }
    },
    {
      name: 'setLocalDescription',
      peerFactory(deferred) {
        return {
          signalingState: 'stable',
          remoteCalls: 0,
          createCalls: 0,
          localCalls: 0,
          async setRemoteDescription() { this.remoteCalls += 1 },
          async createAnswer() { this.createCalls += 1; return { type: 'answer', sdp: 'answer' } },
          async setLocalDescription() { this.localCalls += 1; return deferred.promise }
        }
      },
      expected: { remoteCalls: 1, createCalls: 1, localCalls: 1 }
    }
  ]

  for (const stage of stages) {
    let resolveStage
    const deferred = { promise: new Promise(resolve => { resolveStage = resolve }) }
    const peer = stage.peerFactory(deferred)
    let sends = 0
    const socket = { readyState: 1, send: () => { sends += 1 } }
    const harness = signalOfferHarness({ peer, socket })
    const handling = harness.handleSignal(offerMessage, { socket, sessionPeer: peer })
    while (peer[stage.name === 'setRemoteDescription' ? 'remoteCalls' : stage.name === 'createAnswer' ? 'createCalls' : 'localCalls'] === 0) {
      await Promise.resolve()
    }
    harness.replaceSignalContext()
    resolveStage(stage.name === 'createAnswer' ? { type: 'answer', sdp: 'answer' } : undefined)
    await handling
    assert.equal(peer.remoteCalls, stage.expected.remoteCalls, stage.name)
    assert.equal(peer.createCalls, stage.expected.createCalls, stage.name)
    assert.equal(peer.localCalls, stage.expected.localCalls, stage.name)
    assert.equal(sends, 0, stage.name)
  }
})

test('candidate add and flush stay on the captured peer and stop after context loss', async () => {
  let current = true
  let rejectAdd
  const queued = []
  const candidate = { candidate: 'candidate:current', usernameFragment: 'current' }
  const oldPeer = {
    remoteDescription: { sdp: 'v=0\r\na=ice-ufrag:current\r\n' },
    calls: [],
    addIceCandidate(value) {
      this.calls.push(value)
      return new Promise((_resolve, reject) => { rejectAdd = reject })
    }
  }
  const replacementPeer = { addIceCandidate() { assert.fail('replacement peer must never receive old candidates') } }
  const addHarness = compileFunctions([
    'remoteDescriptionICEUfrags',
    'remoteIceCandidateUsernameFragment',
    'remoteIceCandidateMatchesDescription',
    'queueRemoteIceCandidate',
    'addRemoteIceCandidate'
  ], {
    pc: replacementPeer,
    queuedRemoteCandidates: queued,
    console: { info() {} }
  })
  const adding = addHarness.addRemoteIceCandidate(candidate, oldPeer, () => current)
  current = false
  rejectAdd(new Error('old peer closed'))
  await adding
  assert.deepEqual(oldPeer.calls, [candidate])
  assert.deepEqual(queued, [])

  current = true
  let resolveFirst
  const first = { candidate: 'candidate:first', usernameFragment: 'current' }
  const second = { candidate: 'candidate:second', usernameFragment: 'current' }
  const flushQueue = [{ candidate: first }, { candidate: second }]
  const flushPeer = {
    remoteDescription: oldPeer.remoteDescription,
    calls: [],
    addIceCandidate(value) {
      this.calls.push(value)
      if (value === first) return new Promise(resolve => { resolveFirst = resolve })
      return Promise.resolve()
    }
  }
  const flushHarness = compileFunctions([
    'remoteDescriptionICEUfrags',
    'remoteIceCandidateUsernameFragment',
    'remoteIceCandidateMatchesDescription',
    'flushQueuedRemoteCandidates'
  ], {
    pc: replacementPeer,
    queuedRemoteCandidates: flushQueue,
    console: { warn() {} }
  })
  const flushing = flushHarness.flushQueuedRemoteCandidates(flushPeer, () => current)
  while (!resolveFirst) await Promise.resolve()
  current = false
  resolveFirst()
  await flushing
  assert.deepEqual(flushPeer.calls, [first])
})

test('local-track attachment uses the captured peer and stops after context loss', async () => {
  let current = true
  let resolveFirst
  const videoTrack = { id: 'camera', kind: 'video' }
  const audioTrack = { id: 'microphone', kind: 'audio' }
  const firstSender = {
    track: null,
    calls: [],
    replaceTrack(track) {
      this.calls.push(track)
      return new Promise(resolve => { resolveFirst = resolve })
    }
  }
  const secondSender = {
    track: null,
    calls: [],
    async replaceTrack(track) { this.calls.push(track) }
  }
  const transceivers = [
    { mid: '0', sender: firstSender, direction: 'recvonly' },
    { mid: '1', sender: secondSender, direction: 'recvonly' }
  ]
  const capturedPeer = { getTransceivers: () => transceivers }
  const replacementPeer = { getTransceivers() { assert.fail('replacement peer must not attach old offer tracks') } }
  const { attachLocalTracksToOffer } = compileFunctions(['attachLocalTracksToOffer'], {
    localStream: {},
    pc: replacementPeer,
    remoteMediaSections: () => new Map([
      ['0', { kind: 'video', direction: 'recvonly' }],
      ['1', { kind: 'audio', direction: 'recvonly' }]
    ]),
    applyBrowserVideoCodecPreference() {},
    outboundTrackForKind: kind => kind === 'video' ? videoTrack : audioTrack,
    console: { warn() {} }
  })
  const attaching = attachLocalTracksToOffer({ sdp: 'offer' }, capturedPeer, () => current)
  while (!resolveFirst) await Promise.resolve()
  current = false
  resolveFirst()
  await attaching

  assert.deepEqual(firstSender.calls, [videoTrack])
  assert.equal(transceivers[0].direction, 'recvonly')
  assert.deepEqual(secondSender.calls, [])
})

test('stable media reconciliation does not reparent unchanged PiP, board, or ordered tiles', () => {
  const { reconcileStableChildren } = compileFunctions(['reconcileStableChildren'])
  const operations = []
  const container = {
    children: [],
    insertBefore(child, before) {
      const existing = this.children.indexOf(child)
      if (existing >= 0) this.children.splice(existing, 1)
      const target = before ? this.children.indexOf(before) : this.children.length
      this.children.splice(target < 0 ? this.children.length : target, 0, child)
      operations.push(`insert:${child.id}`)
    }
  }
  const child = id => ({
    id,
    remove() {
      const index = container.children.indexOf(this)
      if (index >= 0) container.children.splice(index, 1)
      operations.push(`remove:${id}`)
    }
  })
  const first = child('first')
  const second = child('second')
  container.children.push(first, second)

  assert.equal(reconcileStableChildren(container, [first, second]), false)
  assert.deepEqual(operations, [])
  assert.equal(reconcileStableChildren(container, [second, first]), true)
  assert.deepEqual(container.children, [second, first])
  const operationCount = operations.length
  assert.equal(reconcileStableChildren(container, [second, first]), false)
  assert.equal(operations.length, operationCount)

  for (const renderer of ['renderPipMeeting', 'renderBoardDock', 'applyVideoTileOrder']) {
    assert.match(functionSource(renderer), /reconcileStableChildren\(/, renderer)
  }
})

test('repeat ontrack restores participant maps after a healthy same-track early return', () => {
  const track = { id: 'video-1', kind: 'video', readyState: 'live', muted: false }
  const stream = { getVideoTracks: () => [track] }
  const video = { srcObject: stream }
  const tile = {
    dataset: { participant: 'Caitlyn' },
    querySelector: selector => selector === 'video' ? video : null
  }
  const remoteStreamsByParticipant = new Map([['Caitlyn', stream]])
  const remoteVideoTracksByParticipant = new Map([['Caitlyn', track]])
  let rebindCalls = 0

  const { addRemoteTrack } = compileFunctions([
    'syncRemoteParticipantVideoMappings',
    'addRemoteTrack'
  ], {
    liveTrack: candidate => Boolean(candidate && candidate.readyState !== 'ended'),
    remoteStreamsByParticipant,
    remoteVideoTracksByParticipant,
    MediaStream: class MediaStream {
      constructor(tracks) { this.tracks = tracks }
    },
    addRemoteAudioTrack() {},
    remoteTrackKeysForEvent: () => ['track:video-1'],
    remoteTrackIdentityKeysForEvent: () => ['track:video-1'],
    participantNameForRemoteTrack: () => 'Caitlyn',
    isCurrentParticipantName: () => false,
    removeRemoteParticipantTrack() {},
    removeRemoteParticipantVideoTracksByName() {
      remoteStreamsByParticipant.delete('Caitlyn')
      remoteVideoTracksByParticipant.delete('Caitlyn')
    },
    remoteTileForKeys: () => tile,
    rebindRemoteVideoTileTrack() {
      rebindCalls += 1
      return false
    },
    rememberRemoteTileKeys() {},
    remoteKeysForTile: () => ['track:video-1'],
    iosScreenStageOwnsParticipant: () => false,
    scheduleAuxiliaryVideoPlaybackRepair() {},
    createRemoteVideoTile() { assert.fail('repeat ontrack must not create a duplicate tile') }
  })

  addRemoteTrack({ track, streams: [stream] })
  assert.equal(rebindCalls, 1)
  assert.equal(remoteStreamsByParticipant.get('Caitlyn'), stream)
  assert.equal(remoteVideoTracksByParticipant.get('Caitlyn'), track)
})

test('intentional leave releases the socket heartbeat before suppressing onclose', () => {
  const leaveSource = functionSource('leaveRoom')
  const clearIndex = leaveSource.indexOf('window.clearInterval(socket.__roomHeartbeat)')
  const suppressIndex = leaveSource.indexOf('socket.onclose = null')

  assert.ok(clearIndex >= 0, 'leaveRoom must clear the socket-owned heartbeat')
  assert.ok(suppressIndex > clearIndex, 'heartbeat cleanup must run before onclose is suppressed')
  assert.match(leaveSource, /socket\.__roomHeartbeat = undefined/)

  const closeHandler = sourceBetween('socket.onclose = event => {', 'socket.onerror =')
  assert.match(closeHandler, /window\.clearInterval\(socket\.__roomHeartbeat\)/)
  assert.match(closeHandler, /socket\.__roomHeartbeat = undefined/)
})
