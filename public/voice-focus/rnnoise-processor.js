const processorName = 'bonfire-rnnoise-processor'
const pcmScale = 32767
const queueCapacity = 1920
// RNNoise is a denoiser, not a gate. The floor never slams to zero: it rides a
// smoothed VAD between 0.5 and 1.0 so word onsets are never chopped. The one
// exception is a soft comfort ducker for SUSTAINED non-speech (keyboard, fan),
// which attenuates at most 12 dB (amplitude 0.251) — a gentle duck, never a mute.
const comfortDuckGain = 0.251

function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value))
}

class BonfireRNNoiseProcessor extends AudioWorkletProcessor {
  constructor(options = {}) {
    super()
    const processorOptions = options.processorOptions || {}
    this.noiseFloor = 0.012
    this.speechFloor = 0.075
    this.strength = 0.998
    this.floorGain = 0.0015
    this.calibrated = false
    this.ready = false
    this.failed = false
    this.sampleRate = Number(processorOptions.sampleRate) || sampleRate || 0
    this.frameSize = 480
    this.inputFrame = new Float32Array(this.frameSize)
    this.inputFill = 0
    this.outputQueue = new Float32Array(queueCapacity)
    this.queueStart = 0
    this.queueLength = 0
    this.rnnoiseGate = 1
    this.rnnoiseHoldFrames = 0
    this.rnnoiseSpeech = 0
    this.noiseBias = 0
    this.fallbackGain = 1
    this.fallbackSpeech = 0
    this.fallbackHoldFrames = 0
    this.metricsFrames = 0
    this.inputMetricSum = 0
    this.outputMetricSum = 0
    this.metricCount = 0
    this.port.onmessage = event => this.handleMessage(event.data || {})
    this.initializeWasm(processorOptions)
  }

  handleMessage(data) {
    if (data.type === 'configure') {
      this.noiseFloor = clamp(Number(data.noiseFloor) || this.noiseFloor, 0.004, 0.12)
      this.speechFloor = clamp(Number(data.speechFloor) || this.speechFloor, 0.02, 0.45)
      this.strength = clamp(Number(data.strength) || this.strength, 0.25, 0.999)
      this.floorGain = clamp(Number(data.floorGain) || this.floorGain, 0.001, 0.16)
      this.calibrated = Boolean(data.calibrated)
      return
    }
    if (data.type === 'destroy') {
      this.destroyRNNoise()
    }
  }

  initializeWasm(options) {
    if (!globalThis.WebAssembly) {
      this.failRNNoise('webassembly-unavailable')
      return
    }

    try {
      if (options.wasmModule) {
        this.installInstance(new WebAssembly.Instance(options.wasmModule, this.wasmImports()))
        return
      }
    } catch (error) {
      this.failRNNoise(error?.message || 'module-instance-failed')
    }

    if (options.wasmBytes) {
      WebAssembly.instantiate(options.wasmBytes, this.wasmImports())
        .then(result => {
          const instance = result?.instance || result
          this.installInstance(instance)
        })
        .catch(error => this.failRNNoise(error?.message || 'wasm-instantiate-failed'))
      return
    }

    this.failRNNoise('wasm-module-missing')
  }

  wasmImports() {
    return {
      a: {
        b: (destination, source, bytes) => {
          if (!this.heapU8) return
          this.heapU8.copyWithin(destination >>> 0, source >>> 0, (source + bytes) >>> 0)
        },
        a: requestedSize => {
          if (!this.memory) return 0
          try {
            const pages = Math.ceil((requestedSize - this.memory.buffer.byteLength) / 65536)
            if (pages > 0) {
              this.memory.grow(pages)
              this.refreshHeap()
            }
            return 1
          } catch {
            return 0
          }
        }
      }
    }
  }

  refreshHeap() {
    this.heapU8 = new Uint8Array(this.memory.buffer)
    this.heapF32 = new Float32Array(this.memory.buffer)
  }

  installInstance(instance) {
    if (!instance?.exports) {
      this.failRNNoise('wasm-instance-missing')
      return
    }
    this.exports = instance.exports
    this.memory = this.exports.c
    if (!this.memory || !this.exports.k || !this.exports.h || !this.exports.l) {
      this.failRNNoise('wasm-exports-missing')
      return
    }
    this.refreshHeap()
    this.exports.d?.()
    this.frameSize = Number(this.exports.f?.()) || 480
    this.inputFrame = new Float32Array(this.frameSize)
    this.inputFill = 0
    this.statePtr = this.exports.h(0)
    this.inputPtr = this.exports.l(this.frameSize * 4)
    this.outputPtr = this.exports.l(this.frameSize * 4)
    if (!this.statePtr || !this.inputPtr || !this.outputPtr) {
      this.failRNNoise('wasm-alloc-failed')
      return
    }
    this.ready = true
    this.failed = false
    this.port.postMessage({ type: 'ready', processor: 'rnnoise-wasm', frameSize: this.frameSize, sampleRate: this.sampleRate })
  }

  failRNNoise(message) {
    this.ready = false
    this.failed = true
    this.port.postMessage({ type: 'error', processor: 'rnnoise-wasm', message: String(message || 'rnnoise-unavailable'), frameSize: this.frameSize, sampleRate: this.sampleRate })
  }

  destroyRNNoise() {
    try {
      if (this.exports?.i && this.statePtr) {
        this.exports.i(this.statePtr)
      }
      if (this.exports?.j && this.inputPtr) {
        this.exports.j(this.inputPtr)
      }
      if (this.exports?.j && this.outputPtr) {
        this.exports.j(this.outputPtr)
      }
    } catch {
      // AudioWorklet cleanup is best-effort.
    }
    this.statePtr = 0
    this.inputPtr = 0
    this.outputPtr = 0
    this.ready = false
  }

  fallbackFrameGain(reference) {
    if (!reference?.length) return 1
    let sum = 0
    let peak = 0
    let crossings = 0
    let previous = reference[0] || 0
    for (let index = 0; index < reference.length; index++) {
      const sample = reference[index] || 0
      const amplitude = Math.abs(sample)
      peak = Math.max(peak, amplitude)
      sum += sample * sample
      if (index > 0 && ((sample >= 0 && previous < 0) || (sample < 0 && previous >= 0))) {
        crossings++
      }
      previous = sample
    }

    const rms = Math.sqrt(sum / Math.max(1, reference.length))
    const zcr = crossings / Math.max(1, reference.length - 1)
    const crest = peak / Math.max(rms, 0.0001)
    const threshold = Math.max(this.noiseFloor * (this.calibrated ? 3.05 : 3.35), this.speechFloor * (this.calibrated ? 0.37 : 0.31), 0.013)
    const closeAt = threshold * 0.58
    const forcedNoise = (crest > 6.4 && rms < threshold * 2.15) || (zcr > 0.28 && rms < threshold * 2.65) || (zcr < 0.006 && rms < threshold * 1.55)
    const levelBlend = clamp((rms - closeAt) / Math.max(0.0001, threshold * 1.35 - closeAt), 0, 1)
    const speechLike = zcr >= 0.006 && zcr <= 0.28 && crest < 8.4
    const instantSpeech = clamp(levelBlend * (speechLike ? 1 : 0.35), 0, 1)
    const open = !forcedNoise && instantSpeech >= 0.34
    if (open) {
      this.fallbackHoldFrames = this.calibrated ? 16 : 12
    } else if (forcedNoise) {
      this.fallbackHoldFrames = 0
    } else if (this.fallbackHoldFrames > 0) {
      this.fallbackHoldFrames--
    }
    this.fallbackSpeech = this.fallbackSpeech * 0.62 + instantSpeech * 0.38
    if (forcedNoise) this.fallbackSpeech *= 0.45

    let targetGain = 1
    if (!open && (this.fallbackHoldFrames <= 0 || forcedNoise)) {
      targetGain = rms <= closeAt || forcedNoise
        ? this.floorGain
        : this.floorGain + clamp((rms - closeAt) / Math.max(0.0001, threshold - closeAt), 0, 1) * (1 - this.floorGain) * (1 - this.strength * 0.52)
    }
    this.fallbackGain += (targetGain - this.fallbackGain) * (targetGain > this.fallbackGain ? 0.68 : (forcedNoise ? 0.58 : 0.28))
    const speechNoiseBlend = Math.max(1 - this.fallbackGain, 1 - this.fallbackSpeech)
    this.noiseBias = Math.min(this.noiseFloor * (0.65 + speechNoiseBlend * 5.0), this.fallbackSpeech > 0.72 ? 0.017 : 0.072) * this.strength
    return this.fallbackGain
  }

  fallbackSample(sample, gain) {
    const denoised = Math.sign(sample) * Math.max(0, Math.abs(sample) - this.noiseBias)
    return denoised * gain
  }

  enqueueOutput(sample) {
    if (this.queueLength >= this.outputQueue.length) {
      this.queueStart = (this.queueStart + 1) % this.outputQueue.length
      this.queueLength--
    }
    const writeIndex = (this.queueStart + this.queueLength) % this.outputQueue.length
    this.outputQueue[writeIndex] = sample
    this.queueLength++
  }

  dequeueOutput() {
    if (this.queueLength <= 0) {
      return undefined
    }
    const sample = this.outputQueue[this.queueStart]
    this.queueStart = (this.queueStart + 1) % this.outputQueue.length
    this.queueLength--
    return sample
  }

  feedRNNoise(sample) {
    this.inputFrame[this.inputFill++] = clamp(sample, -1, 1) * pcmScale
    if (this.inputFill < this.frameSize) {
      return
    }

    const forcedNoise = this.forcedNoiseFrame()
    this.heapF32.set(this.inputFrame, this.inputPtr >> 2)
    const vad = clamp(Number(this.exports.k(this.statePtr, this.outputPtr, this.inputPtr)) || 0, 0, 1)
    // Smooth RNNoise's own VAD. This shapes a gentle floor only — it is never a
    // gate: RNNoise has already denoised the frame, so speech passes continuously.
    this.rnnoiseSpeech = this.rnnoiseSpeech * 0.72 + vad * 0.28
    const smoothedVad = this.rnnoiseSpeech

    // Hold recent speech so trailing consonants survive a momentary VAD dip.
    if (smoothedVad > 0.35 || vad > 0.42) {
      this.rnnoiseHoldFrames = 10
    } else if (this.rnnoiseHoldFrames > 0) {
      this.rnnoiseHoldFrames--
    }

    // Denoiser floor: 0.6 + 0.4·VAD, clamped [0.5, 1.0]. Sustained non-speech
    // (only once the speech hold has expired) gets a soft -12 dB comfort duck,
    // never a full mute — this is what stops word onsets being chopped.
    let targetGate = clamp(0.6 + 0.4 * smoothedVad, 0.5, 1.0)
    if (forcedNoise && this.rnnoiseHoldFrames <= 0) {
      targetGate = comfortDuckGain
    }
    this.rnnoiseGate += (targetGate - this.rnnoiseGate) * (targetGate > this.rnnoiseGate ? 0.44 : 0.3)
    // No second spectral subtraction — RNNoise already removed the noise; biasing
    // its output again only dulls consonants. Kept at 0 for metric continuity.
    this.noiseBias = 0

    const outputOffset = this.outputPtr >> 2
    for (let index = 0; index < this.frameSize; index++) {
      const rnnoiseSample = clamp(this.heapF32[outputOffset + index] / pcmScale, -1, 1)
      this.enqueueOutput(rnnoiseSample * this.rnnoiseGate)
    }
    this.inputFill = 0
  }

  forcedNoiseFrame() {
    let sum = 0
    let peak = 0
    let crossings = 0
    let previous = (this.inputFrame[0] || 0) / pcmScale
    for (let index = 0; index < this.inputFrame.length; index++) {
      const sample = (this.inputFrame[index] || 0) / pcmScale
      const amplitude = Math.abs(sample)
      peak = Math.max(peak, amplitude)
      sum += sample * sample
      if (index > 0 && ((sample >= 0 && previous < 0) || (sample < 0 && previous >= 0))) {
        crossings++
      }
      previous = sample
    }
    const rms = Math.sqrt(sum / Math.max(1, this.inputFrame.length))
    const zcr = crossings / Math.max(1, this.inputFrame.length - 1)
    const crest = peak / Math.max(rms, 0.0001)
    const threshold = Math.max(this.noiseFloor * (this.calibrated ? 3.05 : 3.35), this.speechFloor * (this.calibrated ? 0.37 : 0.31), 0.013)
    return (crest > 6.4 && rms < threshold * 2.15)
      || (zcr > 0.28 && rms < threshold * 2.65)
      || (zcr < 0.006 && rms < threshold * 1.55)
  }

  process(inputs, outputs) {
    const input = inputs[0]
    const output = outputs[0]
    if (!output?.length) {
      return true
    }
    const reference = input?.[0]
    const fallbackGain = this.ready ? 1 : this.fallbackFrameGain(reference)

    for (let channel = 0; channel < output.length; channel++) {
      const target = output[channel]
      const source = input?.[Math.min(channel, Math.max(0, input.length - 1))] || reference
      for (let index = 0; index < target.length; index++) {
        const sample = source?.[index] || 0
        this.inputMetricSum += sample * sample
        let outputSample
        if (this.ready) {
          if (channel === 0) {
            this.feedRNNoise(sample)
          }
          outputSample = this.dequeueOutput()
          if (outputSample === undefined) {
            outputSample = 0
          }
        } else {
          outputSample = this.fallbackSample(sample, fallbackGain)
        }
        target[index] = outputSample
        if (channel === 0) {
          this.outputMetricSum += outputSample * outputSample
          this.metricCount++
        }
      }
    }

    this.metricsFrames++
    if (this.metricsFrames % 48 === 0) {
      const inputRms = Math.sqrt(this.inputMetricSum / Math.max(1, this.metricCount))
      const outputRms = Math.sqrt(this.outputMetricSum / Math.max(1, this.metricCount))
      this.port.postMessage({
        type: 'metrics',
        inputRms,
        outputRms,
        gain: this.ready ? this.rnnoiseGate : this.fallbackGain,
        noiseBias: this.noiseBias,
        speechConfidence: this.ready ? this.rnnoiseSpeech : this.fallbackSpeech,
        processor: this.ready ? 'rnnoise-wasm' : (this.failed ? 'voice-focus-fallback' : 'rnnoise-loading'),
        frameSize: this.frameSize,
        sampleRate: this.sampleRate
      })
      this.inputMetricSum = 0
      this.outputMetricSum = 0
      this.metricCount = 0
    }
    return true
  }
}

registerProcessor(processorName, BonfireRNNoiseProcessor)
