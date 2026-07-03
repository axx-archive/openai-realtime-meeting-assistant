#!/usr/bin/env node

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import vm from 'node:vm'

const rootDir = dirname(dirname(fileURLToPath(import.meta.url)))
const html = readFileSync(join(rootDir, 'index.html'), 'utf8')
// The soft comfort-duck floor for sustained non-speech (-12 dB), mirroring
// rnnoise-processor.js. RNNoise denoises; the gate only shapes a gentle floor.
const comfortDuckGain = 0.251
const functionSource = [
  extractFunction(html, 'clampNumber'),
  extractFunction(html, 'voiceFocusConfig'),
  extractFunction(html, 'createVoiceFocusState'),
  extractFunction(html, 'voiceFocusFrameGain')
].join('\n\n')

const sandbox = {
  Number,
  Math,
  defaultVoiceProfile: {
    noiseFloor: 0.012,
    speechFloor: 0.075,
    trainedAt: ''
  },
  audioSettings: {
    profile: {
      noiseFloor: 0.012,
      speechFloor: 0.075,
      trainedAt: ''
    }
  }
}
vm.createContext(sandbox)
vm.runInContext(functionSource, sandbox)

const requiredInlineSnippets = [
  'floorGain: 0.0015',
  'strength: 0.998',
  'speechConfidence: 0',
  'voiceFocusRNNoiseProcessorName',
  'voiceFocusRNNoiseWasmPath',
  'processor: data.processor || \'rnnoise-wasm\'',
  'const forcedNoise = transient || hissNoise || rumbleNoise',
  'const speechNoiseBlend = Math.max(isolationBlend',
  'const biasMultiplier = 0.65 + speechNoiseBlend * 4.6',
  'state.noiseBias = Math.min(state.noiseFloor * biasMultiplier'
]

const snippetFailures = requiredInlineSnippets.filter(snippet => !html.includes(snippet))
const rnnoiseResults = await benchmarkRNNoiseWasm()
const results = [
  benchmarkNoiseSuppression('steady fan', makeFanFrame, { maxRatio: 0.035, maxGain: 0.025 }),
  benchmarkNoiseSuppression('broadband hiss', makeHissFrame, { maxRatio: 0.055, maxGain: 0.03 }),
  benchmarkTransientSuppression(),
  benchmarkSpeechRecovery(),
  benchmarkNoisySpeechRecovery(),
  benchmarkConcurrentNoiseReduction('speech over fan', makeFanFrame, { maxResidualRatio: 0.95, minSpeechRatio: 0.72 }),
  benchmarkNoiseSuppression('hvac fan', makeHVACFrame, { maxRatio: 0.08, maxGain: 0.05 }),
  benchmarkSoftSpeechRecovery(),
  benchmarkEchoFixture(),
  benchmarkPlosiveFixture(),
  benchmarkOverlappingSpeakersFixture(),
  benchmarkPostSpeechSuppression('post-speech fan', makeFanFrame, { maxRatio: 0.12, maxGain: 0.45 }),
  benchmarkPostSpeechSuppression('post-speech keyboard', makeKeyboardFrame, { maxRatio: 0.12, maxGain: 0.45 }),
  ...rnnoiseResults
]

const failures = [
  ...snippetFailures.map(snippet => `inline voice-focus snippet missing: ${snippet}`),
  ...results.flatMap(result => result.failures)
]

const report = {
  ok: failures.length === 0,
  failures,
  results
}

console.log(JSON.stringify(report, null, 2))
process.exitCode = failures.length === 0 ? 0 : 1

function extractFunction(source, name) {
  const marker = `function ${name}`
  const start = source.indexOf(marker)
  if (start === -1) {
    throw new Error(`missing ${marker}`)
  }
  const open = source.indexOf('{', start)
  if (open === -1) {
    throw new Error(`missing body for ${marker}`)
  }

  let depth = 0
  for (let index = open; index < source.length; index++) {
    const char = source[index]
    if (char === '{') {
      depth++
    } else if (char === '}') {
      depth--
      if (depth === 0) {
        return source.slice(start, index + 1)
      }
    }
  }
  throw new Error(`unterminated ${marker}`)
}

function benchmarkNoiseSuppression(name, frameFactory, limits) {
  const state = sandbox.createVoiceFocusState(sandbox.voiceFocusConfig())
  const frames = []
  for (let frameIndex = 0; frameIndex < 90; frameIndex++) {
    frames.push(processFrame(state, frameFactory(frameIndex)))
  }
  const tail = frames.slice(-20)
  const tailOutputRatio = average(tail.map(frame => frame.outputRms / Math.max(frame.inputRms, 0.000001)))
  const lastGain = tail.at(-1).gain
  const failures = []
  if (tailOutputRatio > limits.maxRatio) {
    failures.push(`${name} output ratio ${tailOutputRatio.toFixed(4)} exceeded ${limits.maxRatio}`)
  }
  if (lastGain > limits.maxGain) {
    failures.push(`${name} gain ${lastGain.toFixed(4)} exceeded ${limits.maxGain}`)
  }
  return {
    name,
    tailOutputRatio,
    lastGain,
    inputRms: average(tail.map(frame => frame.inputRms)),
    outputRms: average(tail.map(frame => frame.outputRms)),
    failures
  }
}

function benchmarkTransientSuppression() {
  const state = sandbox.createVoiceFocusState(sandbox.voiceFocusConfig())
  const frames = []
  for (let frameIndex = 0; frameIndex < 70; frameIndex++) {
    frames.push(processFrame(state, makeKeyboardFrame(frameIndex)))
  }
  const tail = frames.slice(-20)
  const tailOutputRatio = average(tail.map(frame => frame.outputRms / Math.max(frame.inputRms, 0.000001)))
  const lastGain = tail.at(-1).gain
  const failures = []
  if (tailOutputRatio > 0.06) {
    failures.push(`keyboard transient output ratio ${tailOutputRatio.toFixed(4)} exceeded 0.06`)
  }
  if (lastGain > 0.04) {
    failures.push(`keyboard transient gain ${lastGain.toFixed(4)} exceeded 0.04`)
  }
  return {
    name: 'keyboard transients',
    tailOutputRatio,
    lastGain,
    inputRms: average(tail.map(frame => frame.inputRms)),
    outputRms: average(tail.map(frame => frame.outputRms)),
    failures
  }
}

function benchmarkSpeechRecovery() {
  const state = sandbox.createVoiceFocusState(sandbox.voiceFocusConfig())
  for (let frameIndex = 0; frameIndex < 80; frameIndex++) {
    processFrame(state, makeFanFrame(frameIndex))
  }
  const speechFrames = []
  for (let frameIndex = 0; frameIndex < 28; frameIndex++) {
    speechFrames.push(processFrame(state, makeSpeechFrame(frameIndex)))
  }
  const settled = speechFrames.slice(-12)
  const outputRatio = average(settled.map(frame => frame.outputRms / Math.max(frame.inputRms, 0.000001)))
  const averageGain = average(settled.map(frame => frame.gain))
  const failures = []
  if (outputRatio < 0.72) {
    failures.push(`speech output ratio ${outputRatio.toFixed(4)} below 0.72`)
  }
  if (averageGain < 0.82) {
    failures.push(`speech gain ${averageGain.toFixed(4)} below 0.82`)
  }
  if (averageGain > 1.005) {
    failures.push(`speech gain ${averageGain.toFixed(4)} exceeded 1.005`)
  }
  return {
    name: 'speech after noise',
    outputRatio,
    averageGain,
    inputRms: average(settled.map(frame => frame.inputRms)),
    outputRms: average(settled.map(frame => frame.outputRms)),
    failures
  }
}

function benchmarkNoisySpeechRecovery() {
  const state = sandbox.createVoiceFocusState(sandbox.voiceFocusConfig())
  for (let frameIndex = 0; frameIndex < 80; frameIndex++) {
    processFrame(state, makeFanFrame(frameIndex))
  }
  const speechFrames = []
  for (let frameIndex = 0; frameIndex < 28; frameIndex++) {
    speechFrames.push(processFrame(state, mixFrames(makeSpeechFrame(frameIndex), makeFanFrame(frameIndex + 80))))
  }
  const settled = speechFrames.slice(-12)
  const outputRatio = average(settled.map(frame => frame.outputRms / Math.max(frame.inputRms, 0.000001)))
  const averageGain = average(settled.map(frame => frame.gain))
  const averageBias = average(settled.map(frame => frame.noiseBias))
  const failures = []
  if (outputRatio < 0.62) {
    failures.push(`noisy speech output ratio ${outputRatio.toFixed(4)} below 0.62`)
  }
  if (averageGain < 0.82) {
    failures.push(`noisy speech gain ${averageGain.toFixed(4)} below 0.82`)
  }
  if (averageBias < 0.006) {
    failures.push(`noisy speech noise bias ${averageBias.toFixed(4)} below 0.006`)
  }
  return {
    name: 'speech with fan',
    outputRatio,
    averageGain,
    averageBias,
    inputRms: average(settled.map(frame => frame.inputRms)),
    outputRms: average(settled.map(frame => frame.outputRms)),
    failures
  }
}

function benchmarkSoftSpeechRecovery() {
  const state = sandbox.createVoiceFocusState(sandbox.voiceFocusConfig())
  for (let frameIndex = 0; frameIndex < 90; frameIndex++) {
    processFrame(state, makeHVACFrame(frameIndex))
  }
  const speechFrames = []
  for (let frameIndex = 0; frameIndex < 34; frameIndex++) {
    speechFrames.push(processFrame(state, mixFrames(makeSoftSpeechFrame(frameIndex), makeHVACFrame(frameIndex + 90))))
  }
  const settled = speechFrames.slice(-14)
  const outputRatio = average(settled.map(frame => frame.outputRms / Math.max(frame.inputRms, 0.000001)))
  const averageGain = average(settled.map(frame => frame.gain))
  const failures = []
  if (outputRatio < 0.45) {
    failures.push(`soft speech output ratio ${outputRatio.toFixed(4)} below 0.45`)
  }
  if (averageGain < 0.62) {
    failures.push(`soft speech gain ${averageGain.toFixed(4)} below 0.62`)
  }
  return {
    name: 'soft speech with hvac',
    outputRatio,
    averageGain,
    inputRms: average(settled.map(frame => frame.inputRms)),
    outputRms: average(settled.map(frame => frame.outputRms)),
    failures
  }
}

function benchmarkEchoFixture() {
  const state = sandbox.createVoiceFocusState(sandbox.voiceFocusConfig())
  const frames = []
  let previousSpeech = makeSilentFrame()
  for (let frameIndex = 0; frameIndex < 42; frameIndex++) {
    const speech = makeSpeechFrame(frameIndex)
    const echo = previousSpeech.map(sample => sample * 0.28)
    frames.push(processFrame(state, mixFrames(speech, echo)))
    previousSpeech = speech
  }
  const settled = frames.slice(-16)
  const outputRatio = average(settled.map(frame => frame.outputRms / Math.max(frame.inputRms, 0.000001)))
  const averageGain = average(settled.map(frame => frame.gain))
  const failures = []
  if (outputRatio < 0.68) {
    failures.push(`echo fixture output ratio ${outputRatio.toFixed(4)} below 0.68`)
  }
  if (averageGain < 0.75) {
    failures.push(`echo fixture gain ${averageGain.toFixed(4)} below 0.75`)
  }
  return {
    name: 'speech with echo tail',
    outputRatio,
    averageGain,
    failures
  }
}

function benchmarkPlosiveFixture() {
  const state = sandbox.createVoiceFocusState(sandbox.voiceFocusConfig())
  const frames = []
  for (let frameIndex = 0; frameIndex < 38; frameIndex++) {
    frames.push(processFrame(state, mixFrames(makeSpeechFrame(frameIndex), makePlosiveFrame(frameIndex))))
  }
  const settled = frames.slice(-16)
  const outputRatio = average(settled.map(frame => frame.outputRms / Math.max(frame.inputRms, 0.000001)))
  const averageGain = average(settled.map(frame => frame.gain))
  const failures = []
  if (outputRatio < 0.58) {
    failures.push(`plosive fixture output ratio ${outputRatio.toFixed(4)} below 0.58`)
  }
  if (averageGain < 0.7) {
    failures.push(`plosive fixture gain ${averageGain.toFixed(4)} below 0.70`)
  }
  return {
    name: 'speech with plosives',
    outputRatio,
    averageGain,
    failures
  }
}

function benchmarkOverlappingSpeakersFixture() {
  const state = sandbox.createVoiceFocusState(sandbox.voiceFocusConfig())
  const frames = []
  for (let frameIndex = 0; frameIndex < 40; frameIndex++) {
    frames.push(processFrame(state, mixFrames(makeSpeechFrame(frameIndex), makeSecondSpeakerFrame(frameIndex))))
  }
  const settled = frames.slice(-16)
  const outputRatio = average(settled.map(frame => frame.outputRms / Math.max(frame.inputRms, 0.000001)))
  const averageGain = average(settled.map(frame => frame.gain))
  const failures = []
  if (outputRatio < 0.64) {
    failures.push(`overlapping speakers output ratio ${outputRatio.toFixed(4)} below 0.64`)
  }
  if (averageGain < 0.74) {
    failures.push(`overlapping speakers gain ${averageGain.toFixed(4)} below 0.74`)
  }
  return {
    name: 'overlapping speakers',
    outputRatio,
    averageGain,
    failures
  }
}

function benchmarkConcurrentNoiseReduction(name, noiseFactory, limits) {
  const cleanState = sandbox.createVoiceFocusState(sandbox.voiceFocusConfig())
  const noisyState = sandbox.createVoiceFocusState(sandbox.voiceFocusConfig())
  for (let frameIndex = 0; frameIndex < 80; frameIndex++) {
    processFrame(cleanState, noiseFactory(frameIndex))
    processFrame(noisyState, noiseFactory(frameIndex))
  }

  const frames = []
  for (let frameIndex = 0; frameIndex < 28; frameIndex++) {
    const speech = makeSpeechFrame(frameIndex)
    const noise = noiseFactory(frameIndex + 80)
    const clean = processFrame(cleanState, speech)
    const noisy = processFrame(noisyState, mixFrames(speech, noise))
    const residual = noisy.output.map((sample, index) => sample - (clean.output[index] || 0))
    frames.push({
      residualRms: rms(residual),
      noiseRms: rms(noise),
      speechRms: rms(speech),
      cleanOutputRms: clean.outputRms,
      noisyOutputRms: noisy.outputRms,
      noiseBias: noisy.noiseBias
    })
  }

  const settled = frames.slice(-12)
  const residualRatio = average(settled.map(frame => frame.residualRms / Math.max(frame.noiseRms, 0.000001)))
  const cleanSpeechRatio = average(settled.map(frame => frame.cleanOutputRms / Math.max(frame.speechRms, 0.000001)))
  const averageBias = average(settled.map(frame => frame.noiseBias))
  const failures = []
  if (residualRatio > limits.maxResidualRatio) {
    failures.push(`${name} residual ratio ${residualRatio.toFixed(4)} exceeded ${limits.maxResidualRatio}`)
  }
  if (cleanSpeechRatio < limits.minSpeechRatio) {
    failures.push(`${name} clean speech ratio ${cleanSpeechRatio.toFixed(4)} below ${limits.minSpeechRatio}`)
  }
  return {
    name,
    residualRatio,
    cleanSpeechRatio,
    averageBias,
    residualRms: average(settled.map(frame => frame.residualRms)),
    noiseRms: average(settled.map(frame => frame.noiseRms)),
    failures
  }
}

function benchmarkPostSpeechSuppression(name, frameFactory, limits) {
  const state = sandbox.createVoiceFocusState(sandbox.voiceFocusConfig())
  for (let frameIndex = 0; frameIndex < 18; frameIndex++) {
    processFrame(state, mixFrames(makeSpeechFrame(frameIndex), makeFanFrame(frameIndex)))
  }
  const noiseFrames = []
  for (let frameIndex = 0; frameIndex < 12; frameIndex++) {
    noiseFrames.push(processFrame(state, frameFactory(frameIndex + 18)))
  }
  const firstEight = noiseFrames.slice(0, 8)
  const outputRatio = average(firstEight.map(frame => frame.outputRms / Math.max(frame.inputRms, 0.000001)))
  const maxGain = Math.max(...firstEight.map(frame => frame.gain))
  const failures = []
  if (outputRatio > limits.maxRatio) {
    failures.push(`${name} output ratio ${outputRatio.toFixed(4)} exceeded ${limits.maxRatio}`)
  }
  if (maxGain > limits.maxGain) {
    failures.push(`${name} gain ${maxGain.toFixed(4)} exceeded ${limits.maxGain}`)
  }
  return {
    name,
    outputRatio,
    maxGain,
    inputRms: average(firstEight.map(frame => frame.inputRms)),
    outputRms: average(firstEight.map(frame => frame.outputRms)),
    failures
  }
}

function processFrame(state, input) {
  const gain = sandbox.voiceFocusFrameGain(state, input)
  const noiseBias = state.noiseBias || 0
  const output = input.map(sample => {
    const denoised = Math.sign(sample) * Math.max(0, Math.abs(sample) - noiseBias)
    return denoised * gain
  })
  return {
    gain,
    inputRms: rms(input),
    outputRms: rms(output),
    noiseBias,
    output
  }
}

async function benchmarkRNNoiseWasm() {
  const wasmPath = join(rootDir, 'public/voice-focus/rnnoise.wasm')
  const concurrent = await benchmarkRNNoiseConcurrentNoise(wasmPath)
  // Denoiser bars, not slam-gate bars (rtc §2.2): steady fan must still shed real
  // dB via RNNoise; sparse keyboard clicks the gate no longer chops just need to
  // stay bounded (RNNoise, not the gate, owns transient removal now).
  const fan = await benchmarkRNNoiseNoiseOnly(wasmPath, 'rnnoise fan-only gate', rnnoiseFanSample, { maxRatio: 0.55, minSuppressionDb: 4 })
  const keyboard = await benchmarkRNNoiseNoiseOnly(wasmPath, 'rnnoise keyboard-only gate', rnnoiseKeyboardSample, { maxRatio: 1.6 })
  const non48k = await benchmarkRNNoiseNon48kHz(wasmPath)
  const onset = await benchmarkSpeechOnsetPreservation(wasmPath)
  return [concurrent, fan, keyboard, non48k, onset]
}

// Proves §2.2: the denoiser floor preserves the leading consonant of a word that
// starts suddenly over noise, where the retired slam-gate chopped it. Runs the
// SAME fixture through the old gate and the new one and asserts the new retains
// more onset energy (and enough of it in absolute terms).
async function benchmarkSpeechOnsetPreservation(wasmPath) {
  const sampleRate = 48000
  const runGate = async legacyGate => {
    const processor = await createRNNoiseProcessor(wasmPath, { legacyGate })
    for (let frameIndex = 0; frameIndex < 40; frameIndex++) {
      processor.process(makeRNNoiseFrame(processor.frameSize, sampleRate, frameIndex, rnnoiseFanSample))
    }
    const onsetFrames = []
    for (let frameIndex = 0; frameIndex < 6; frameIndex++) {
      const speech = makeRNNoiseFrame(processor.frameSize, sampleRate, frameIndex, rnnoiseSpeechSample)
      const noise = makeRNNoiseFrame(processor.frameSize, sampleRate, frameIndex + 200, rnnoiseFanSample)
      const input = mixFramesByIndex(speech, noise)
      const result = processor.process(input)
      onsetFrames.push({ outputRms: rms(result.output), speechRms: rms(speech), gate: result.gate })
    }
    return onsetFrames
  }
  const legacy = await runGate(true)
  const current = await runGate(false)
  const window = 3
  const legacyRetained = average(legacy.slice(0, window).map(frame => frame.outputRms / Math.max(frame.speechRms, 0.000001)))
  const currentRetained = average(current.slice(0, window).map(frame => frame.outputRms / Math.max(frame.speechRms, 0.000001)))
  const failures = []
  if (!(currentRetained > legacyRetained)) {
    failures.push(`speech-onset energy retained ${currentRetained.toFixed(4)} not above legacy gate ${legacyRetained.toFixed(4)}`)
  }
  if (currentRetained < 0.4) {
    failures.push(`speech-onset energy retained ${currentRetained.toFixed(4)} below 0.40 — onset still chopped`)
  }
  return {
    name: 'speech-onset preservation (new vs old gate)',
    legacyRetained,
    currentRetained,
    onsetImprovement: currentRetained / Math.max(legacyRetained, 0.000001),
    firstFrameGateNew: current[0].gate,
    firstFrameGateOld: legacy[0].gate,
    failures
  }
}

function suppressionDb(inputRms, outputRms) {
  if (!(inputRms > 0) || !(outputRms > 0)) {
    return inputRms > 0 ? 80 : 0
  }
  return Math.max(0, 20 * Math.log10(inputRms / outputRms))
}

async function createRNNoiseProcessor(wasmPath, options = {}) {
  const legacyGate = Boolean(options.legacyGate)
  let memory
  let heapU8
  let heapF32
  const refreshHeap = () => {
    heapU8 = new Uint8Array(memory.buffer)
    heapF32 = new Float32Array(memory.buffer)
  }
  const imports = {
    a: {
      b: (destination, source, bytes) => {
        heapU8.copyWithin(destination >>> 0, source >>> 0, (source + bytes) >>> 0)
      },
      a: requestedSize => {
        try {
          const pages = Math.ceil((requestedSize - memory.buffer.byteLength) / 65536)
          if (pages > 0) {
            memory.grow(pages)
            refreshHeap()
          }
          return 1
        } catch {
          return 0
        }
      }
    }
  }
  const { instance } = await WebAssembly.instantiate(readFileSync(wasmPath), imports)
  const exports = instance.exports
  memory = exports.c
  refreshHeap()
  exports.d?.()
  const frameSize = Number(exports.f?.()) || 480
  const statePtr = exports.h(0)
  const inputPtr = exports.l(frameSize * 4)
  const outputPtr = exports.l(frameSize * 4)
  const state = {
    rnnoiseSpeech: 0,
    rnnoiseHoldFrames: 0,
    rnnoiseGate: 1,
    noiseBias: 0,
    noiseFloor: 0.012,
    strength: 0.998,
    floorGain: 0.0015
  }

  return {
    frameSize,
    process(input) {
      for (let index = 0; index < frameSize; index++) {
        heapF32[(inputPtr >> 2) + index] = clampBenchmark(input[index] || 0, -1, 1) * 32767
      }
      const forcedNoise = rnnoiseForcedNoiseFrame(input)
      const vad = clampBenchmark(Number(exports.k(statePtr, outputPtr, inputPtr)) || 0, 0, 1)

      if (legacyGate) {
        // The retired slam-gate ladder, kept only so the A/B onset benchmark can
        // prove the new denoiser floor stopped chopping word onsets.
        const frameVoice = rnnoiseFrameVoiceConfidence(input)
        state.rnnoiseSpeech = state.rnnoiseSpeech * 0.72 + vad * 0.28
        if (forcedNoise) {
          state.rnnoiseSpeech *= 0.25
          state.rnnoiseHoldFrames = 0
        } else if (frameVoice > 0.42 || (state.rnnoiseSpeech > 0.45 && frameVoice > 0.24)) {
          state.rnnoiseHoldFrames = 10
        } else if (state.rnnoiseHoldFrames > 0) {
          state.rnnoiseHoldFrames--
        }
        const targetGate = forcedNoise
          ? state.floorGain
          : state.rnnoiseHoldFrames > 0
          ? 1
          : (frameVoice < 0.24 ? state.floorGain : 0.32)
        state.rnnoiseGate += (targetGate - state.rnnoiseGate) * (targetGate > state.rnnoiseGate ? 0.44 : 0.3)
        state.noiseBias = Math.min(state.noiseFloor * (state.rnnoiseSpeech > 0.55 ? 0.55 : 0.82), state.rnnoiseSpeech > 0.55 ? 0.0085 : 0.016) * state.strength
        const output = new Array(frameSize)
        for (let index = 0; index < frameSize; index++) {
          const rnnoiseSample = clampBenchmark(heapF32[(outputPtr >> 2) + index] / 32767, -1, 1)
          const biased = Math.sign(rnnoiseSample) * Math.max(0, Math.abs(rnnoiseSample) - state.noiseBias)
          output[index] = biased * state.rnnoiseGate
        }
        return { output, vad, gate: state.rnnoiseGate, speech: state.rnnoiseSpeech, noiseBias: state.noiseBias }
      }

      // Current denoiser floor (mirrors rnnoise-processor.js): never slams to
      // zero, never a second spectral subtraction — RNNoise is the denoiser.
      state.rnnoiseSpeech = state.rnnoiseSpeech * 0.72 + vad * 0.28
      const smoothedVad = state.rnnoiseSpeech
      if (smoothedVad > 0.35 || vad > 0.42) {
        state.rnnoiseHoldFrames = 10
      } else if (state.rnnoiseHoldFrames > 0) {
        state.rnnoiseHoldFrames--
      }
      let targetGate = clampBenchmark(0.6 + 0.4 * smoothedVad, 0.5, 1.0)
      if (forcedNoise && state.rnnoiseHoldFrames <= 0) {
        targetGate = comfortDuckGain
      }
      state.rnnoiseGate += (targetGate - state.rnnoiseGate) * (targetGate > state.rnnoiseGate ? 0.44 : 0.3)
      state.noiseBias = 0
      const output = new Array(frameSize)
      for (let index = 0; index < frameSize; index++) {
        const rnnoiseSample = clampBenchmark(heapF32[(outputPtr >> 2) + index] / 32767, -1, 1)
        output[index] = rnnoiseSample * state.rnnoiseGate
      }
      return { output, vad, gate: state.rnnoiseGate, speech: state.rnnoiseSpeech, noiseBias: state.noiseBias }
    }
  }
}

async function benchmarkRNNoiseConcurrentNoise(wasmPath) {
  const cleanProcessor = await createRNNoiseProcessor(wasmPath)
  const noisyProcessor = await createRNNoiseProcessor(wasmPath)
  const sampleRate = 48000
  const prerollIn = []
  const prerollOut = []
  for (let frameIndex = 0; frameIndex < 100; frameIndex++) {
    const noise = makeRNNoiseFrame(cleanProcessor.frameSize, sampleRate, frameIndex, rnnoiseFanSample)
    cleanProcessor.process(noise)
    const noisyResult = noisyProcessor.process(noise)
    prerollIn.push(rms(noise))
    prerollOut.push(rms(noisyResult.output))
  }
  // Before/after on the noise-only preroll: the real dB RNNoise sheds from steady fan.
  const noiseSuppressionDb = suppressionDb(average(prerollIn.slice(-40)), average(prerollOut.slice(-40)))

  const residualRatios = []
  const speechRatios = []
  const biases = []
  for (let frameIndex = 0; frameIndex < 100; frameIndex++) {
    const speech = makeRNNoiseFrame(cleanProcessor.frameSize, sampleRate, frameIndex, rnnoiseSpeechSample)
    const noise = makeRNNoiseFrame(cleanProcessor.frameSize, sampleRate, frameIndex + 100, rnnoiseFanSample)
    const clean = cleanProcessor.process(speech)
    const noisy = noisyProcessor.process(mixFramesByIndex(speech, noise))
    const residual = noisy.output.map((sample, index) => sample - (clean.output[index] || 0))
    residualRatios.push(rms(residual) / Math.max(rms(noise), 0.000001))
    speechRatios.push(rms(clean.output) / Math.max(rms(speech), 0.000001))
    biases.push(noisy.noiseBias)
  }
  const settledResidual = residualRatios.slice(-40)
  const settledSpeech = speechRatios.slice(-40)
  const residualRatio = average(settledResidual)
  const speechRatio = average(settledSpeech)
  const failures = []
  // Gentle floor leaks more noise during speech than the old slam-gate did — the
  // trade that stops word onsets from being chopped — so this bar is honestly higher.
  if (residualRatio > 0.85) {
    failures.push(`rnnoise speech over fan residual ratio ${residualRatio.toFixed(4)} exceeded 0.85`)
  }
  if (speechRatio < 0.8) {
    failures.push(`rnnoise speech over fan speech ratio ${speechRatio.toFixed(4)} below 0.80`)
  }
  return {
    name: 'rnnoise speech over fan',
    residualRatio,
    speechRatio,
    noiseSuppressionDb,
    averageBias: average(biases.slice(-40)),
    failures
  }
}

async function benchmarkRNNoiseNoiseOnly(wasmPath, name, sampleFactory, limits) {
  const processor = await createRNNoiseProcessor(wasmPath)
  const sampleRate = 48000
  const ratios = []
  const gates = []
  const inputRmsValues = []
  const outputRmsValues = []
  for (let frameIndex = 0; frameIndex < 130; frameIndex++) {
    const input = makeRNNoiseFrame(processor.frameSize, sampleRate, frameIndex, sampleFactory)
    const result = processor.process(input)
    ratios.push(rms(result.output) / Math.max(rms(input), 0.000001))
    gates.push(result.gate)
    inputRmsValues.push(rms(input))
    outputRmsValues.push(rms(result.output))
  }
  const tail = ratios.slice(-40)
  const tailGate = gates.slice(-40)
  const outputRatio = average(tail)
  const averageGate = average(tailGate)
  // Honest before/after: the denoiser floor never slams to silence, so steady
  // noise is reduced by RNNoise itself (positive dB) while sparse transients the
  // gate intentionally no longer chops read near 0 dB — that trade is the point.
  const noiseSuppressionDb = suppressionDb(average(inputRmsValues.slice(-40)), average(outputRmsValues.slice(-40)))
  const failures = []
  if (outputRatio > limits.maxRatio) {
    failures.push(`${name} output ratio ${outputRatio.toFixed(4)} exceeded ${limits.maxRatio}`)
  }
  if (limits.minSuppressionDb !== undefined && noiseSuppressionDb < limits.minSuppressionDb) {
    failures.push(`${name} suppression ${noiseSuppressionDb.toFixed(2)} dB below ${limits.minSuppressionDb} dB`)
  }
  return {
    name,
    outputRatio,
    averageGate,
    noiseSuppressionDb,
    failures
  }
}

async function benchmarkRNNoiseNon48kHz(wasmPath) {
  const processor = await createRNNoiseProcessor(wasmPath)
  const sampleRate = 44100
  const ratios = []
  for (let frameIndex = 0; frameIndex < 80; frameIndex++) {
    const speech = makeRNNoiseFrame(processor.frameSize, sampleRate, frameIndex, rnnoiseSpeechSample)
    const result = processor.process(speech)
    const ratio = rms(result.output) / Math.max(rms(speech), 0.000001)
    if (Number.isFinite(ratio)) {
      ratios.push(ratio)
    }
  }
  const outputRatio = average(ratios.slice(-30))
  const failures = []
  if (!Number.isFinite(outputRatio) || outputRatio <= 0) {
    failures.push('rnnoise non-48khz produced no finite output')
  }
  return {
    name: 'rnnoise non-48khz speech path',
    sampleRate,
    outputRatio,
    failures
  }
}

function rnnoiseForcedNoiseFrame(frame) {
  let sum = 0
  let peak = 0
  let crossings = 0
  let previous = frame[0] || 0
  for (let index = 0; index < frame.length; index++) {
    const sample = frame[index] || 0
    const amplitude = Math.abs(sample)
    peak = Math.max(peak, amplitude)
    sum += sample * sample
    if (index > 0 && ((sample >= 0 && previous < 0) || (sample < 0 && previous >= 0))) {
      crossings++
    }
    previous = sample
  }
  const frameRms = Math.sqrt(sum / Math.max(1, frame.length))
  const zcr = crossings / Math.max(1, frame.length - 1)
  const crest = peak / Math.max(frameRms, 0.0001)
  const threshold = Math.max(0.012 * 3.35, 0.075 * 0.31, 0.013)
  return (crest > 6.4 && frameRms < threshold * 2.15)
    || (zcr > 0.28 && frameRms < threshold * 2.65)
    || (zcr < 0.006 && frameRms < threshold * 1.55)
}

function rnnoiseFrameVoiceConfidence(frame) {
  let sum = 0
  let peak = 0
  let crossings = 0
  let previous = frame[0] || 0
  for (let index = 0; index < frame.length; index++) {
    const sample = frame[index] || 0
    const amplitude = Math.abs(sample)
    peak = Math.max(peak, amplitude)
    sum += sample * sample
    if (index > 0 && ((sample >= 0 && previous < 0) || (sample < 0 && previous >= 0))) {
      crossings++
    }
    previous = sample
  }
  const frameRms = Math.sqrt(sum / Math.max(1, frame.length))
  const zcr = crossings / Math.max(1, frame.length - 1)
  const crest = peak / Math.max(frameRms, 0.0001)
  const threshold = Math.max(0.012 * 3.35, 0.075 * 0.31, 0.013)
  const closeAt = threshold * 0.58
  const levelBlend = clampBenchmark((frameRms - closeAt) / Math.max(0.0001, threshold * 1.35 - closeAt), 0, 1)
  const speechLike = zcr >= 0.006 && zcr <= 0.28 && crest < 8.4
  const steadyVoice = frameRms >= threshold * 1.08 && crest < 5.8 && zcr < 0.28
  const peakSpeech = peak >= threshold * 2.5 && frameRms >= closeAt && crest < 7.2 && zcr < 0.28
  return clampBenchmark(levelBlend * (speechLike || steadyVoice ? 1 : 0.2) + (peakSpeech ? 0.28 : 0), 0, 1)
}

function makeRNNoiseFrame(length, sampleRate, frameIndex, sampleFactory) {
  const frame = new Array(length)
  for (let index = 0; index < frame.length; index++) {
    const sampleIndex = frameIndex * length + index
    frame[index] = sampleFactory(sampleIndex / sampleRate, sampleIndex)
  }
  return frame
}

function rnnoiseFanSample(t) {
  return Math.sin(2 * Math.PI * 170 * t) * 0.012
    + Math.sin(2 * Math.PI * 340 * t) * 0.006
    + Math.sin(2 * Math.PI * 72 * t) * 0.005
}

function rnnoiseKeyboardSample(t, sampleIndex) {
  const click = sampleIndex % 1200 === 0 ? 0.16 : sampleIndex % 1200 === 1 ? -0.07 : 0
  return click + Math.sin(2 * Math.PI * 420 * t) * 0.003
}

function rnnoiseSpeechSample(t) {
  const envelope = 0.8 + Math.sin(2 * Math.PI * 4 * t) * 0.2
  return envelope * (
    Math.sin(2 * Math.PI * 145 * t) * 0.055
    + Math.sin(2 * Math.PI * 290 * t) * 0.035
    + Math.sin(2 * Math.PI * 870 * t) * 0.016
  )
}

function mixFramesByIndex(...frames) {
  return frames[0].map((_, index) => frames.reduce((total, frame) => total + (frame[index] || 0), 0))
}

function clampBenchmark(value, min, max) {
  return Math.min(max, Math.max(min, value))
}

function makeFanFrame(frameIndex) {
  return makeFrame((index, t) => {
    return Math.sin(2 * Math.PI * 90 * t) * 0.019
      + Math.sin(2 * Math.PI * 180 * t + frameIndex * 0.07) * 0.006
  })
}

function makeHissFrame(frameIndex) {
  let seed = 0x9e3779b9 ^ frameIndex
  return makeFrame(() => {
    seed ^= seed << 13
    seed ^= seed >>> 17
    seed ^= seed << 5
    return (((seed >>> 0) / 0xffffffff) * 2 - 1) * 0.018
  })
}

function makeKeyboardFrame(frameIndex) {
  const clickOffset = (frameIndex * 37) % 512
  return makeFrame((index, t) => {
    const click = index === clickOffset ? 0.18 : index === clickOffset + 1 ? -0.08 : 0
    return click + Math.sin(2 * Math.PI * 420 * t) * 0.003
  })
}

function makeHVACFrame(frameIndex) {
  return makeFrame((index, t) => {
    return Math.sin(2 * Math.PI * 58 * t + frameIndex * 0.03) * 0.012
      + Math.sin(2 * Math.PI * 117 * t) * 0.008
      + Math.sin(2 * Math.PI * 240 * t + 0.7) * 0.005
  })
}

function makeSpeechFrame(frameIndex) {
  return makeFrame((index, t) => {
    const envelope = 0.92 + Math.sin((frameIndex * 512 + index) / 900) * 0.08
    return envelope * (
      Math.sin(2 * Math.PI * 190 * t) * 0.076
      + Math.sin(2 * Math.PI * 760 * t + 0.6) * 0.026
      + Math.sin(2 * Math.PI * 1320 * t + 1.2) * 0.018
    )
  })
}

function makeSoftSpeechFrame(frameIndex) {
  return makeFrame((index, t) => {
    const envelope = 0.86 + Math.sin((frameIndex * 512 + index) / 1200) * 0.1
    return envelope * (
      Math.sin(2 * Math.PI * 175 * t) * 0.052
      + Math.sin(2 * Math.PI * 700 * t + 0.4) * 0.02
      + Math.sin(2 * Math.PI * 1180 * t + 1.1) * 0.013
    )
  })
}

function makeSecondSpeakerFrame(frameIndex) {
  return makeFrame((index, t) => {
    const envelope = 0.8 + Math.sin((frameIndex * 512 + index) / 760) * 0.12
    return envelope * (
      Math.sin(2 * Math.PI * 132 * t + 0.5) * 0.042
      + Math.sin(2 * Math.PI * 510 * t + 0.2) * 0.019
      + Math.sin(2 * Math.PI * 980 * t + 1.5) * 0.012
    )
  })
}

function makePlosiveFrame(frameIndex) {
  return makeFrame((index, t) => {
    const burst = frameIndex % 9 === 0 && index < 32
      ? Math.exp(-index / 9) * 0.11
      : 0
    return burst + Math.sin(2 * Math.PI * 92 * t) * 0.002
  })
}

function makeSilentFrame() {
  return makeFrame(() => 0)
}

function mixFrames(...frames) {
  return makeFrame(index => frames.reduce((total, frame) => total + (frame[index] || 0), 0))
}

function makeFrame(sampleAt) {
  const frame = new Array(512)
  const sampleRate = 48000
  for (let index = 0; index < frame.length; index++) {
    frame[index] = sampleAt(index, index / sampleRate)
  }
  return frame
}

function rms(samples) {
  const sum = samples.reduce((total, sample) => total + sample * sample, 0)
  return Math.sqrt(sum / Math.max(1, samples.length))
}

function average(values) {
  return values.reduce((total, value) => total + value, 0) / Math.max(1, values.length)
}
