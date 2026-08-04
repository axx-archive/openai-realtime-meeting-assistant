const assert = require('node:assert/strict')
const fs = require('node:fs')
const test = require('node:test')
const vm = require('node:vm')
const window = { setTimeout, clearTimeout }
const navigator = { mediaDevices: { getUserMedia: async () => { throw new Error('unexpected capture') } } }
vm.runInNewContext(fs.readFileSync(require.resolve('../public/composer-dictation.js'), 'utf8'), { window, globalThis: window, navigator, Blob })
const { AudioFocusCoordinator, nextDictationState } = window.StrideComposerDictation

function extractFunction(source, signature) {
  const start = source.indexOf(signature)
  assert.notEqual(start, -1, `missing ${signature}`)
  const open = source.indexOf('{', start)
  let depth = 0
  for (let index = open; index < source.length; index += 1) {
    if (source[index] === '{') depth += 1
    if (source[index] === '}') depth -= 1
    if (depth === 0) return source.slice(start, index + 1)
  }
  throw new Error(`unterminated ${signature}`)
}

test('one Send advances an active recording through held into transcribing', async () => {
  let state = 'idle'
  state = nextDictationState(state, 'record')
  assert.equal(state, 'recording')
  const { ComposerDictationController } = window.StrideComposerDictation
  const controller = Object.create(ComposerDictationController.prototype)
  const transitions = []
  controller.state = state
  controller.stop = async () => {
    controller.state = nextDictationState(controller.state, 'stop')
    transitions.push(controller.state)
  }
  controller.send = async () => {
    controller.state = nextDictationState(controller.state, 'send')
    transitions.push(controller.state)
  }
  await controller.commit()
  assert.deepEqual(transitions, ['held', 'transcribing'])
  assert.equal(controller.state, 'transcribing')
  assert.equal(nextDictationState('held', 'complete'), 'held')
})

test('focus invalidates an old microphone owner before awaiting its close', async () => {
  const focus = new AudioFocusCoordinator()
  let close
  let entered
  const enteredClose = new Promise((resolve) => { entered = resolve })
  const personal = await focus.acquire('personal_realtime', {
    close: () => new Promise((resolve) => { close = resolve; entered(); })
  })
  const dictation = focus.acquire('composer_dictation')
  await enteredClose
  assert.equal(personal.isCurrent(), false)
  close()
  assert.equal((await dictation).isCurrent(), true)
})

test('overlapping takeovers close the superseded pending web owner exactly once', async () => {
  const focus = new AudioFocusCoordinator()
  const terminal = []
  let allowFirstClose
  let firstCloseEntered
  const enteredClose = new Promise((resolve) => { firstCloseEntered = resolve })
  const first = await focus.acquire('composer_dictation', {
    close: (reason) => new Promise((resolve) => {
      terminal.push(`first:${reason}`)
      allowFirstClose = resolve
      firstCloseEntered()
    })
  })

  const middlePromise = focus.acquire('personal_realtime', {
    close: (reason) => { terminal.push(`middle:${reason}`) }
  })
  await enteredClose
  const latestPromise = focus.acquire('meeting_media', {
    close: (reason) => { terminal.push(`latest:${reason}`) }
  })
  assert.equal(first.isCurrent(), false)

  allowFirstClose()
  const [middle, latest] = await Promise.all([middlePromise, latestPromise])
  assert.equal(middle.isCurrent(), false, 'the middle lease was never granted after the newer intent')
  assert.equal(await middle.release(), false)
  assert.equal(latest.isCurrent(), true)
  assert.deepEqual(terminal, [
    'first:superseded_by_personal_realtime',
    'middle:superseded_by_meeting_media'
  ])
})

test('web focus release is idempotent and invokes close once', async () => {
  const focus = new AudioFocusCoordinator()
  const terminal = []
  const lease = await focus.acquire('personal_realtime', {
    close: (reason) => { terminal.push(reason) }
  })
  const release = lease.release('completed')
  const duplicate = lease.release('cancelled')
  assert.equal(lease.isCurrent(), false)
  assert.equal(await release, true)
  assert.equal(await duplicate, false)
  assert.equal(await lease.release('error'), false)
  assert.deepEqual(terminal, ['completed'])
})

test('a failed predecessor close aborts the web request, closes it once, and leaves the queue usable', async () => {
  const focus = new AudioFocusCoordinator()
  const terminal = []
  await focus.acquire('composer_dictation', {
    close: (reason) => {
      terminal.push(`first:${reason}`)
      throw new Error('close failed')
    }
  })
  await assert.rejects(
    focus.acquire('personal_realtime', {
      close: (reason) => { terminal.push(`pending:${reason}`) }
    }),
    /close failed/
  )
  assert.deepEqual(terminal, [
    'first:superseded_by_personal_realtime',
    'pending:error'
  ])
  const recovered = await focus.acquire('meeting_media')
  assert.equal(recovered.isCurrent(), true)
})

test('a stale web composer lease aborts before pre-capture work or getUserMedia', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  const controller = Object.create(ComposerDictationController.prototype)
  let beforeCaptureCalls = 0
  let getUserMediaCalls = 0
  let releases = 0
  window.MediaRecorder = function MediaRecorder() {}
  navigator.mediaDevices.getUserMedia = async () => { getUserMediaCalls += 1; return {} }
  controller.state = 'idle'
  controller.input = { disabled: false }
  controller.focus = {
    acquire: async () => ({
      isCurrent: () => false,
      release: async () => { releases += 1; return false }
    })
  }
  controller.beforeCapture = async () => { beforeCaptureCalls += 1; return null }
  controller.afterCapture = async () => {}
  controller.render = () => {}

  await controller.start()
  assert.equal(beforeCaptureCalls, 0)
  assert.equal(getUserMediaCalls, 0)
  assert.equal(releases, 1)
  assert.equal(controller.lease, null)
})

test('supersession during web pre-capture restores state before aborting capture', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  const controller = Object.create(ComposerDictationController.prototype)
  let currentChecks = 0
  let getUserMediaCalls = 0
  let restored = null
  window.MediaRecorder = function MediaRecorder() {}
  navigator.mediaDevices.getUserMedia = async () => { getUserMediaCalls += 1; return {} }
  controller.state = 'idle'
  controller.input = { disabled: false }
  controller.focus = {
    acquire: async () => ({
      isCurrent: () => ++currentChecks === 1,
      release: async () => false
    })
  }
  controller.beforeCapture = async () => ({ priorMuted: true })
  controller.afterCapture = async (value) => { restored = value }
  controller.cleanupCapture = ComposerDictationController.prototype.cleanupCapture.bind(controller)
  controller.render = () => {}

  await controller.start()
  assert.equal(getUserMediaCalls, 0)
  assert.equal(restored?.priorMuted, true)
  assert.equal(controller.lease, null)
})

test('supersession while getUserMedia is pending stops the late stream and restores pre-capture state', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  const controller = Object.create(ComposerDictationController.prototype)
  let currentChecks = 0
  let releases = 0
  let stopped = 0
  let restored = null
  window.MediaRecorder = function MediaRecorder() {}
  navigator.mediaDevices.getUserMedia = async () => ({
    getTracks: () => [{ stop: () => { stopped += 1 } }]
  })
  controller.state = 'idle'
  controller.input = { disabled: false }
  controller.focus = {
    acquire: async () => ({
      isCurrent: () => ++currentChecks < 3,
      release: async () => { releases += 1; return false }
    })
  }
  controller.beforeCapture = async () => ({ priorMuted: false })
  controller.afterCapture = async (value) => { restored = value }
  controller.cleanupCapture = ComposerDictationController.prototype.cleanupCapture.bind(controller)
  controller.render = () => {}

  await controller.start()
  assert.equal(stopped, 1)
  assert.equal(restored?.priorMuted, false)
  assert.equal(releases, 1)
  assert.equal(controller.lease, null)
  assert.equal(controller.lastTerminalReason, 'stale_generation')
})

test('a focus takeover waits for pending getUserMedia cleanup before granting the next owner', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  const focus = new AudioFocusCoordinator()
  const controller = Object.create(ComposerDictationController.prototype)
  let resolveCapture
  let captureEntered
  let meetingSettled = false
  let stopped = 0
  const enteredCapture = new Promise((resolve) => { captureEntered = resolve })
  window.MediaRecorder = function MediaRecorder() {}
  navigator.mediaDevices.getUserMedia = () => new Promise((resolve) => {
    resolveCapture = resolve
    captureEntered()
  })
  controller.state = 'idle'
  controller.startAttempt = null
  controller.input = { disabled: false }
  controller.focus = focus
  controller.beforeCapture = async () => null
  controller.afterCapture = async () => {}
  controller.render = () => {}

  const start = controller.start()
  await enteredCapture
  const meetingPromise = focus.acquire('meeting_media').then((lease) => {
    meetingSettled = true
    return lease
  })
  await Promise.resolve()
  assert.equal(meetingSettled, false, 'new focus must wait for the unresolved old capture boundary')
  assert.equal(stopped, 0)

  resolveCapture({ getTracks: () => [{ stop: () => { stopped += 1 } }] })
  await start
  const meeting = await meetingPromise
  assert.equal(stopped, 1)
  assert.equal(meeting.isCurrent(), true)
})

test('room parking cancels a web dictation intent while focus acquisition is still pending', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  const focus = new AudioFocusCoordinator()
  let allowPriorClose
  let priorCloseEntered
  const entered = new Promise((resolve) => { priorCloseEntered = resolve })
  await focus.acquire('personal_realtime', {
    close: () => new Promise((resolve) => {
      allowPriorClose = resolve
      priorCloseEntered()
    })
  })

  const controller = Object.create(ComposerDictationController.prototype)
  let getUserMediaCalls = 0
  window.MediaRecorder = function MediaRecorder() {}
  navigator.mediaDevices.getUserMedia = async () => {
    getUserMediaCalls += 1
    return { getTracks: () => [] }
  }
  controller.state = 'idle'
  controller.startAttempt = null
  controller.stopPromise = null
  controller.input = { disabled: false }
  controller.focus = focus
  controller.beforeCapture = async () => null
  controller.afterCapture = async () => {}
  controller.render = () => {}

  const start = controller.start()
  await entered
  let parkSettled = false
  const park = controller.park('superseded_by_meeting_media').then(() => { parkSettled = true })
  await Promise.resolve()
  assert.equal(parkSettled, false, 'parking drains the pending acquire instead of returning early')
  assert.equal(getUserMediaCalls, 0)

  allowPriorClose()
  await Promise.all([start, park])
  assert.equal(getUserMediaCalls, 0)
  assert.equal(controller.state, 'idle')
  assert.equal(controller.startAttempt, null)
  assert.equal(focus.active, null)
})

test('a web start cleanup failure settles its attempt and cannot deadlock the next focus owner', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  const focus = new AudioFocusCoordinator()
  let resolveCapture
  let captureEntered
  const entered = new Promise((resolve) => { captureEntered = resolve })
  window.MediaRecorder = function MediaRecorder() {}
  navigator.mediaDevices.getUserMedia = () => new Promise((resolve) => {
    resolveCapture = resolve
    captureEntered()
  })

  const controller = Object.create(ComposerDictationController.prototype)
  controller.state = 'idle'
  controller.startAttempt = null
  controller.stopPromise = null
  controller.input = { disabled: false }
  controller.focus = focus
  controller.beforeCapture = async () => ({ priorMuted: false })
  controller.afterCapture = async () => { throw new Error('restore failed') }
  controller.render = () => {}

  const startResult = controller.start().then(
    () => ({ error: null }),
    (error) => ({ error }),
  )
  await entered
  const meetingPromise = focus.acquire('meeting_media')
  resolveCapture({ getTracks: () => [{ stop: () => {} }] })

  const [{ error }, meeting] = await Promise.all([startResult, meetingPromise])
  assert.match(String(error?.message), /restore failed/)
  assert.equal(controller.startAttempt, null)
  assert.equal(meeting.isCurrent(), true)
})

test('concurrent web Stop calls share one recorder stop, cleanup, and lease release', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  const controller = Object.create(ComposerDictationController.prototype)
  let stopListener
  let nativeStops = 0
  let cleanups = 0
  let releases = 0
  controller.state = 'recording'
  controller.stopPromise = null
  controller.startedAt = Date.now() - 1000
  controller.chunks = [new Blob(['audio'])]
  controller.recorder = {
    state: 'recording',
    mimeType: 'audio/webm',
    addEventListener: (_type, listener) => { stopListener = listener },
    stop: () => { nativeStops += 1 }
  }
  controller.cleanupCapture = async () => { cleanups += 1 }
  controller.render = () => {}
  controller.lease = { release: async () => { releases += 1; return true } }

  const first = controller.stop()
  const second = controller.stop()
  assert.equal(nativeStops, 1)
  stopListener()
  await Promise.all([first, second])
  assert.equal(cleanups, 1)
  assert.equal(releases, 1)
  assert.equal(controller.state, 'held')
})

test('normal web Stop cannot deadlock when exact focus release re-enters its park hook', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  const focus = new AudioFocusCoordinator()
  const controller = Object.create(ComposerDictationController.prototype)
  controller.state = 'recording'
  controller.stopPromise = null
  controller.startedAt = Date.now() - 1000
  controller.chunks = [new Blob(['captured-audio'])]
  let stopListener
  controller.recorder = {
    state: 'recording',
    mimeType: 'audio/webm',
    addEventListener: (type, listener) => { if (type === 'stop') stopListener = listener },
    stop: () => { stopListener() }
  }
  controller.cleanupCapture = async () => {}
  controller.render = () => {}
  const lease = await focus.acquire('composer_dictation', {
    close: () => controller.park('completed')
  })
  controller.lease = lease

  await controller.stop()
  assert.equal(controller.state, 'held')
  assert.equal(controller.stopPromise, null)
  assert.equal(focus.active, null)
})

test('a throwing browser recorder stop still cleans media, releases focus, and retains captured audio', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  const controller = Object.create(ComposerDictationController.prototype)
  let cleanups = 0
  const releases = []
  controller.state = 'recording'
  controller.stopPromise = null
  controller.startedAt = Date.now() - 1000
  controller.chunks = [new Blob(['retained-audio'])]
  controller.recorder = {
    state: 'recording',
    mimeType: 'audio/webm',
    addEventListener: () => {},
    stop: () => { throw new Error('InvalidStateError') }
  }
  controller.cleanupCapture = async () => { cleanups += 1 }
  controller.render = () => {}
  controller.lease = { release: async (reason) => { releases.push(reason); return true } }

  await controller.stop()
  assert.equal(cleanups, 1)
  assert.deepEqual(releases, ['error'])
  assert.equal(controller.state, 'held')
  assert.ok(controller.clip.size > 0)
  assert.match(controller.errorMessage, /clip is saved/)
})

test('an already-inactive browser recorder drains without calling stop again', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  const controller = Object.create(ComposerDictationController.prototype)
  let nativeStops = 0
  let releases = 0
  controller.state = 'recording'
  controller.stopPromise = null
  controller.startedAt = Date.now() - 1000
  controller.chunks = [new Blob(['already-stopped-audio'])]
  controller.recorder = {
    state: 'inactive',
    mimeType: 'audio/webm',
    addEventListener: () => {},
    stop: () => { nativeStops += 1 }
  }
  controller.cleanupCapture = async () => {}
  controller.render = () => {}
  controller.lease = { release: async (reason) => { assert.equal(reason, 'completed'); releases += 1; return true } }

  await controller.stop()
  assert.equal(nativeStops, 0)
  assert.equal(releases, 1)
  assert.equal(controller.state, 'held')
})

test('a browser recorder that emits no stop event times out into bounded cleanup', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  const controller = Object.create(ComposerDictationController.prototype)
  let cleanups = 0
  const releases = []
  controller.state = 'recording'
  controller.stopPromise = null
  controller.stopTimeoutMs = 5
  controller.startedAt = Date.now() - 1000
  controller.chunks = [new Blob(['timed-out-audio'])]
  controller.recorder = {
    state: 'recording',
    mimeType: 'audio/webm',
    addEventListener: () => {},
    stop: () => {}
  }
  controller.cleanupCapture = async () => { cleanups += 1 }
  controller.render = () => {}
  controller.lease = { release: async (reason) => { releases.push(reason); return true } }

  await controller.stop()
  assert.equal(cleanups, 1)
  assert.deepEqual(releases, ['error'])
  assert.equal(controller.state, 'held')
  assert.match(controller.errorMessage, /clip is saved/)
})

test('successive web dictations each submit exactly once', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  const controller = Object.create(ComposerDictationController.prototype)
  let submits = 0
  let transcript = 0
  controller.state = 'held'
  controller.clip = new Blob(['first'])
  controller.durationMs = 900
  controller.generation = 0
  controller.submittedGeneration = 0
  controller.input = { value: '' }
  controller.form = { requestSubmit: () => { submits += 1 } }
  controller.render = () => {}
  controller.transcribe = async () => `dictated ${++transcript}`

  await controller.send()
  controller.state = 'held'
  controller.clip = new Blob(['second'])
  await controller.send()
  assert.equal(submits, 2)
  assert.equal(controller.generation, 2)
  assert.equal(controller.submittedGeneration, 2)
})

test('a blank successful web transcript keeps the exact clip retryable', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  const controller = Object.create(ComposerDictationController.prototype)
  let submits = 0
  const clip = new Blob(['spoken audio'])
  controller.state = 'held'
  controller.clip = clip
  controller.durationMs = 8000
  controller.generation = 0
  controller.submittedGeneration = 0
  controller.input = { value: '' }
  controller.form = { requestSubmit: () => { submits += 1 } }
  controller.render = () => {}
  controller.transcribe = async () => '   '

  await controller.send()
  assert.equal(controller.state, 'held')
  assert.equal(controller.clip, clip)
  assert.equal(submits, 0)
  assert.match(controller.errorMessage, /recording is saved/)
})

test('stale private Realtime attempt cleanup cannot close or clear its replacement session', () => {
  const html = fs.readFileSync(require.resolve('../index.html'), 'utf8')
  const closeAttempt = extractFunction(html, 'function closePrivateRealtimeVoiceAttempt(attempt)')
  const events = []
  const oldChannel = { close: () => { events.push('old-channel') } }
  const oldPeer = { close: () => { events.push('old-peer') } }
  const oldStream = { getTracks: () => [{ stop: () => { events.push('old-stream') } }] }
  const oldCapture = { getTracks: () => [{ stop: () => { events.push('old-capture') } }] }
  const oldProcessor = {}
  const oldTap = {}
  const replacementChannel = {}
  const replacementPeer = {}
  const replacementStream = {}
  const replacementProcessor = {}
  const context = {
    privateRealtimeVoiceDataChannel: replacementChannel,
    privateRealtimeVoicePeer: replacementPeer,
    privateRealtimeVoiceStream: replacementStream,
    privateRealtimeVoiceProcessor: replacementProcessor,
    cleanupAudioProcessor: (processor) => { if (processor === oldProcessor) events.push('old-processor') },
    closeExactStrideSignalTap: (tap) => { if (tap === oldTap) events.push('old-tap') },
  }
  vm.runInNewContext(`${closeAttempt}; this.closeAttempt = closePrivateRealtimeVoiceAttempt`, context)
  context.closeAttempt({
    dataChannel: oldChannel,
    peer: oldPeer,
    stream: oldStream,
    captureStream: oldCapture,
    processor: oldProcessor,
    humanTap: oldTap,
  })

  assert.equal(context.privateRealtimeVoiceDataChannel, replacementChannel)
  assert.equal(context.privateRealtimeVoicePeer, replacementPeer)
  assert.equal(context.privateRealtimeVoiceStream, replacementStream)
  assert.equal(context.privateRealtimeVoiceProcessor, replacementProcessor)
  assert.deepEqual(events, [
    'old-channel',
    'old-peer',
    'old-stream',
    'old-processor',
    'old-capture',
    'old-tap',
  ])

  const begin = extractFunction(html, 'async function beginPrivateRealtimeVoiceSession(sessionToken)')
  assert.doesNotMatch(begin, /assertPrivateRealtimeVoiceSession\([^\n]+closePrivateRealtimeVoiceSession/)
  const start = extractFunction(html, 'async function startPrivateRealtimeVoiceConversation()')
  assert.match(start, /finally \{\s*if \(sessionToken === privateRealtimeVoiceSessionToken\)/)
  const provider = extractFunction(html, 'function handlePrivateRealtimeVoiceEvent(raw, sessionToken, peer)')
  assert.match(provider, /if \(type === 'error'\)[\s\S]*terminatePrivateRealtimeVoiceSession\(sessionToken, peer, message\)[\s\S]*return/)
})

test('a deterministic fake transcript submits the ordinary form exactly once', async () => {
  const { ComposerDictationController } = window.StrideComposerDictation
  let submits = 0
  const controller = Object.create(ComposerDictationController.prototype)
  controller.state = 'held'
  controller.clip = new Blob(['audio'])
  controller.durationMs = 900
  controller.generation = 0
  controller.submittedGeneration = 0
  controller.input = { value: '' }
  controller.form = { requestSubmit: () => { submits += 1 } }
  controller.render = () => {}
  controller.transcribe = async () => 'dictated message'
  await controller.send()
  assert.equal(controller.input.value, 'dictated message')
  assert.equal(submits, 1)
  assert.equal(controller.state, 'idle')
})
