#!/usr/bin/env node

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import vm from 'node:vm'

const rootDir = dirname(dirname(fileURLToPath(import.meta.url)))
const html = readFileSync(join(rootDir, 'index.html'), 'utf8')
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
  'const forcedNoise = transient || hissNoise || rumbleNoise',
  'const speechNoiseBlend = Math.max(isolationBlend',
  'const biasMultiplier = 0.65 + speechNoiseBlend * 4.6',
  'state.noiseBias = Math.min(state.noiseFloor * biasMultiplier'
]

const snippetFailures = requiredInlineSnippets.filter(snippet => !html.includes(snippet))
const results = [
  benchmarkNoiseSuppression('steady fan', makeFanFrame, { maxRatio: 0.035, maxGain: 0.025 }),
  benchmarkNoiseSuppression('broadband hiss', makeHissFrame, { maxRatio: 0.055, maxGain: 0.03 }),
  benchmarkTransientSuppression(),
  benchmarkSpeechRecovery(),
  benchmarkNoisySpeechRecovery(),
  benchmarkConcurrentNoiseReduction('speech over fan', makeFanFrame, { maxResidualRatio: 0.95, minSpeechRatio: 0.72 }),
  benchmarkPostSpeechSuppression('post-speech fan', makeFanFrame, { maxRatio: 0.12, maxGain: 0.45 }),
  benchmarkPostSpeechSuppression('post-speech keyboard', makeKeyboardFrame, { maxRatio: 0.12, maxGain: 0.45 })
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
