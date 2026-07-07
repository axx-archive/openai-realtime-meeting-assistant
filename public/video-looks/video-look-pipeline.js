// Bonfire video "looks" pipeline (Spectacular OS, Wave 13).
//
// A separate, cacheable ES module — mirroring how rnnoise-processor.js keeps the
// audio worklet out of the 34.9k-line monolith. It wraps an outbound camera track
// so the FAR END sees a GPU-cheap enhancement (no ML segmentation / blur):
//
//   camera track
//     -> MediaStreamTrackProcessor  (readable VideoFrames)          [Chrome/Edge/Android]
//     -> OffscreenCanvas + WebGL2 fragment shader (one draw/frame)
//     -> MediaStreamTrackGenerator  (writable) -> processed track
//
// Safari desktop lacks insertable streams, so it falls back to drawing the source
// <video> onto a WebGL2 canvas on a rAF loop and publishing canvas.captureStream(30)
// — the far end still sees the look, at slightly higher cost. Where neither is
// available the pipeline reports tier 'none' and the caller keeps the raw track,
// applying an equivalent CSS filter to the LOCAL PREVIEW ONLY (honest: preview only).
//
// Every look is a preset of the single parameterized shader (look.frag), so a look
// change is a uniform swap. `intensity` (0..1) scales the whole look toward identity.
// A self-contained thermal governor sheds intensity -> look -> (signals audio) on a
// sustained per-frame budget breach and auto-restores after a cool-off, updating an
// honest status the whole time. Any pipeline exception tears down to the raw track —
// never a black tile.

const SHADER_URL = '/public/video-looks/look.frag'

const VERTEX_SRC = `#version 300 es
in vec2 aPos;
out vec2 vUv;
void main() {
  // Fullscreen triangle; flip Y so the sampled frame is upright.
  vUv = vec2((aPos.x + 1.0) * 0.5, 1.0 - (aPos.y + 1.0) * 0.5);
  gl_Position = vec4(aPos, 0.0, 1.0);
}`

// Identity uniform set — every field the shader reads, at its no-op value.
const IDENTITY = {
  uBrightness: 0, uContrast: 1, uSaturation: 1, uTemperature: 0, uGamma: 1,
  uVignette: 0, uSoftClip: 0, uMono: 0, uSCurve: 0, uSharpen: 0,
  uBlackLift: 0, uGrain: 0, uDenoise: 0, uLowLightGain: 0, uTargetLuma: 0.5
}

// The four named looks (rtc §3.2 recipes) as uniform presets over one shader.
export const VIDEO_LOOK_PRESETS = {
  none: { ...IDENTITY },
  // Bonfire warm — flattering, on-brand amber warmth.
  'bonfire-warm': {
    ...IDENTITY,
    uTemperature: 0.28, uSaturation: 1.08, uContrast: 1.05, uGamma: 0.96,
    uSoftClip: 0.35, uVignette: 0.06
  },
  // Studio — clean, neutral, crisp.
  studio: {
    ...IDENTITY,
    uContrast: 1.12, uSaturation: 1.02, uSharpen: 0.45, uBlackLift: 0.02
  },
  // Mono — editorial black & white.
  mono: {
    ...IDENTITY,
    uMono: 1, uContrast: 1.15, uSCurve: 0.35, uGrain: 0.18
  },
  // Low-light boost — rescue a dim room.
  lowlight: {
    ...IDENTITY,
    uGamma: 0.78, uLowLightGain: 0.7, uTargetLuma: 0.55, uDenoise: 0.5, uSaturation: 1.04
  },
  // Background blur — person-segmentation defocus of the BACKGROUND only. Unlike the
  // grading looks this needs an ML person-confidence mask per frame (blur-segmenter.js),
  // so it is a "segmented look": insertable tier only, and the thermal governor sheds its
  // segmentation cost first. uBlurRadius/uMaskFeather are extra (non-IDENTITY) uniforms.
  blur: {
    ...IDENTITY,
    uBlurRadius: 14, uMaskFeather: 0.1
  }
}

// Looks that require per-frame person segmentation (not a pure uniform swap): the caller
// must init/teardown a segmenter across a transition into or out of these, and they can
// only run on the insertable tier this wave.
export const SEGMENTED_LOOKS = new Set(['blur'])

export function isSegmentedLook(look) {
  return SEGMENTED_LOOKS.has(look)
}

export function isKnownLook(look) {
  return Object.prototype.hasOwnProperty.call(VIDEO_LOOK_PRESETS, look)
}

// Feature-detect the best available tier without constructing a pipeline.
export function videoLookPipelineTier() {
  if (!hasWebGL2()) {
    return 'none'
  }
  if (typeof MediaStreamTrackProcessor !== 'undefined'
    && typeof MediaStreamTrackGenerator !== 'undefined'
    && typeof OffscreenCanvas !== 'undefined') {
    return 'insertable'
  }
  if (typeof HTMLCanvasElement !== 'undefined'
    && typeof HTMLCanvasElement.prototype.captureStream === 'function') {
    return 'canvas'
  }
  return 'none'
}

function hasWebGL2() {
  try {
    const probe = typeof OffscreenCanvas !== 'undefined'
      ? new OffscreenCanvas(2, 2)
      : (typeof document !== 'undefined' ? document.createElement('canvas') : null)
    if (!probe) {
      return false
    }
    const gl = probe.getContext('webgl2')
    return Boolean(gl)
  } catch (_) {
    return false
  }
}

let cachedShaderSrc = null
async function loadFragmentSource() {
  if (cachedShaderSrc) {
    return cachedShaderSrc
  }
  const response = await fetch(SHADER_URL, { cache: 'force-cache' })
  if (!response.ok) {
    throw new Error(`look shader ${response.status}`)
  }
  cachedShaderSrc = await response.text()
  return cachedShaderSrc
}

const MAX_DIMENSION = 1280 // hard cap: never upscale in-shader, never exceed capture res
const CONSECUTIVE_ERROR_LIMIT = 6
// Run person segmentation every Nth frame (<=15 mask fps at 30 fps video) and reuse the
// mask in between — segmentation is the expensive part of blur, so this is the first
// thing the governor sheds.
const MASK_INTERVAL_FRAMES = 2

export class VideoLookPipeline {
  constructor(options = {}) {
    this.onStatus = typeof options.onStatus === 'function' ? options.onStatus : () => {}
    this.onError = typeof options.onError === 'function' ? options.onError : () => {}
    // Governor escalation hooks the caller owns (audio shed lives on the audio side).
    this.onShedAudio = typeof options.onShedAudio === 'function' ? options.onShedAudio : () => {}
    this.onRestoreAudio = typeof options.onRestoreAudio === 'function' ? options.onRestoreAudio : () => {}

    this.frameBudgetMs = Number(options.frameBudgetMs) || 14
    this.coolMs = Number(options.coolMs) || 60000

    this.tier = 'none'
    this.active = false
    this.look = 'none'
    this.intensity = 1
    this._effectiveIntensity = 1

    this._gl = null
    this._canvas = null
    this._program = null
    this._uniformLocations = {}
    this._texture = null
    this._uniforms = { ...IDENTITY }

    this._sourceTrack = null
    this._outputTrack = null

    // insertable-streams state
    this._processor = null
    this._generator = null
    this._reader = null
    this._writer = null

    // canvas-capture state
    this._srcVideo = null
    this._capStream = null
    this._raf = 0

    // governor
    this._emaMs = 0
    this._hotFrames = 0
    this._coolFrames = 0
    this._governorStage = 0 // 0 normal · 1 intensity shed · 2 look paused · 3 audio shed
    this._battery = null
    this._consecutiveErrors = 0

    // Segmented-look (blur) state: the person-segmentation wrapper, its mask texture
    // (unit 1), a "have we ever uploaded a mask" flag, and the frame counter + interval
    // that drive the mask cadence (halved by the governor under load).
    this._segmenter = null
    this._maskTexture = null
    this._hasMask = false
    this._frameCount = 0
    this._maskInterval = MASK_INTERVAL_FRAMES
    // One-shot terminal guard: once stop()/_fail() settles the pipeline, no later
    // teardown race (a post-cancel reader read resolving done, a closing writer
    // rejecting) can fire a second — and possibly false — terminal status.
    this._settled = false
    this._boundRafTick = () => this._rafTick()
  }

  get outputTrack() {
    return this._outputTrack
  }

  get status() {
    return {
      tier: this.tier,
      active: this.active && this.tier !== 'none',
      look: this.look,
      intensity: this.intensity,
      governorStage: this._governorStage,
      paused: this._governorStage >= 2
    }
  }

  // Build the pipeline on `sourceTrack` and return the processed outbound track,
  // or null when this browser can only preview (tier 'none' — caller keeps raw).
  async start(sourceTrack, look, intensity) {
    if (!sourceTrack) {
      return null
    }
    this.tier = videoLookPipelineTier()
    if (this.tier === 'none') {
      this._emit('preview-only')
      return null
    }
    this._sourceTrack = sourceTrack
    this.setLook(look, intensity)

    // Segmented looks (background blur) need a person-segmentation mask on the SAME frames
    // the shader composits. Only the insertable tier can feed the segmenter deterministically
    // this wave; anything else throws so the caller falls back to the raw camera with an
    // honest status (never a fake whole-frame blur). The heavy MediaPipe wasm is fetched
    // lazily, only here, only when blur is actually selected.
    if (isSegmentedLook(this.look)) {
      if (this.tier !== 'insertable') {
        throw new Error('blur requires insertable streams')
      }
      const { createBlurSegmenter } = await import('./blur-segmenter.js')
      this._segmenter = await createBlurSegmenter()
    }

    const fragSrc = await loadFragmentSource()
    this._initGL(fragSrc)
    this._readBattery()

    this.active = true
    try {
      if (this.tier === 'insertable') {
        this._outputTrack = await this._startInsertable(sourceTrack)
      } else {
        this._outputTrack = await this._startCanvas(sourceTrack)
      }
    } catch (error) {
      this.active = false
      this._teardownGraph()
      throw error
    }
    if (!this._outputTrack) {
      throw new Error('look pipeline produced no track')
    }
    this._emit('active')
    return this._outputTrack
  }

  // Uniform swap only — no pipeline rebuild, no track replacement. `overrides` lets
  // the settings sliders (brightness / warmth) tune specific uniforms on top of the
  // chosen preset, so the sliders ARE the preset exposed (ux §6.2).
  setLook(look, intensity, overrides) {
    this.look = isKnownLook(look) ? look : 'none'
    if (typeof intensity === 'number') {
      this.intensity = Math.max(0, Math.min(1, intensity))
    }
    this._uniforms = { ...(VIDEO_LOOK_PRESETS[this.look] || IDENTITY) }
    if (overrides && typeof overrides === 'object') {
      for (const key of Object.keys(overrides)) {
        if (Object.prototype.hasOwnProperty.call(IDENTITY, key) && typeof overrides[key] === 'number') {
          this._uniforms[key] = overrides[key]
        }
      }
    }
    // A governor-driven intensity shed persists until cool-off; otherwise honour
    // the caller's chosen intensity.
    if (this._governorStage < 1) {
      this._effectiveIntensity = this.look === 'none' ? 0 : this.intensity
    }
  }

  stop() {
    if (this._settled) {
      return
    }
    this._settled = true
    this.active = false
    this._teardownGraph()
    this._governorStage = 0
    this._hotFrames = 0
    this._coolFrames = 0
    this._emit('off')
  }

  // --- WebGL2 setup ---------------------------------------------------------

  _initGL(fragSrc) {
    this._canvas = this.tier === 'insertable'
      ? new OffscreenCanvas(2, 2)
      : document.createElement('canvas')
    const gl = this._canvas.getContext('webgl2', {
      alpha: false, antialias: false, depth: false, desynchronized: true,
      preserveDrawingBuffer: this.tier === 'canvas'
    })
    if (!gl) {
      throw new Error('WebGL2 unavailable for look pipeline')
    }
    this._gl = gl

    // Context loss makes every GL call a silent no-op (the spec does NOT throw), so
    // _draw would keep "succeeding" while streaming undefined/blank pixels to the far
    // end. Detect it explicitly and fail over to the raw camera — the never-black-tile
    // guarantee (rtc §3.4). Works on OffscreenCanvas (insertable) and canvas (Safari).
    this._canvas.addEventListener('webglcontextlost', event => {
      event.preventDefault?.()
      this._fail(new Error('webgl context lost'))
    })

    const vert = this._compile(gl.VERTEX_SHADER, VERTEX_SRC)
    const frag = this._compile(gl.FRAGMENT_SHADER, fragSrc)
    const program = gl.createProgram()
    gl.attachShader(program, vert)
    gl.attachShader(program, frag)
    gl.bindAttribLocation(program, 0, 'aPos')
    gl.linkProgram(program)
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
      const log = gl.getProgramInfoLog(program)
      throw new Error(`look shader link failed: ${log}`)
    }
    gl.deleteShader(vert)
    gl.deleteShader(frag)
    this._program = program
    gl.useProgram(program)

    const buffer = gl.createBuffer()
    gl.bindBuffer(gl.ARRAY_BUFFER, buffer)
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW)
    gl.enableVertexAttribArray(0)
    gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0)

    this._texture = gl.createTexture()
    gl.bindTexture(gl.TEXTURE_2D, this._texture)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)

    // Second texture (unit 1): the person-confidence mask for segmented looks. LINEAR so
    // the 256² mask upsamples smoothly to the frame; seeded 1×1=255 (subject everywhere →
    // no blur) so it is a valid sampler and never blurs the whole frame before the first
    // real mask arrives.
    this._maskTexture = gl.createTexture()
    gl.activeTexture(gl.TEXTURE1)
    gl.bindTexture(gl.TEXTURE_2D, this._maskTexture)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
    gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1)
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.R8, 1, 1, 0, gl.RED, gl.UNSIGNED_BYTE, new Uint8Array([255]))
    gl.activeTexture(gl.TEXTURE0)

    // Cache every uniform location once.
    const names = ['uTex', 'uTexel', 'uIntensity', 'uBypass',
      'uMask', 'uHasMask', 'uBlurRadius', 'uMaskFeather', ...Object.keys(IDENTITY), 'uTime']
    for (const name of names) {
      this._uniformLocations[name] = gl.getUniformLocation(program, name)
    }
    gl.uniform1i(this._uniformLocations.uTex, 0)
    gl.uniform1i(this._uniformLocations.uMask, 1)
  }

  _compile(type, src) {
    const gl = this._gl
    const shader = gl.createShader(type)
    gl.shaderSource(shader, src)
    gl.compileShader(shader)
    if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
      const log = gl.getShaderInfoLog(shader)
      gl.deleteShader(shader)
      throw new Error(`look shader compile failed: ${log}`)
    }
    return shader
  }

  _sizeFor(width, height) {
    const w = Math.max(2, width || 2)
    const h = Math.max(2, height || 2)
    const scale = Math.min(1, MAX_DIMENSION / Math.max(w, h)) // cap, never upscale
    return { w: Math.round(w * scale), h: Math.round(h * scale) }
  }

  // Draw one source (VideoFrame or HTMLVideoElement) through the look shader.
  _draw(source, width, height) {
    const gl = this._gl
    const { w, h } = this._sizeFor(width, height)
    if (this._canvas.width !== w || this._canvas.height !== h) {
      this._canvas.width = w
      this._canvas.height = h
    }
    gl.viewport(0, 0, w, h)
    gl.activeTexture(gl.TEXTURE0)
    gl.bindTexture(gl.TEXTURE_2D, this._texture)
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, source)

    const loc = this._uniformLocations
    const bypass = this._governorStage >= 2 ? 1 : 0
    gl.uniform1f(loc.uIntensity, this._effectiveIntensity)
    gl.uniform1f(loc.uBypass, bypass)
    gl.uniform2f(loc.uTexel, 1 / w, 1 / h)
    gl.uniform1f(loc.uTime, (performance.now() / 1000) % 1000)
    // Segmented-look (blur) uniforms — no-ops for the grading looks (uHasMask stays 0).
    gl.uniform1f(loc.uHasMask, this._segmenter && this._hasMask ? 1 : 0)
    gl.uniform1f(loc.uBlurRadius, this._uniforms.uBlurRadius || 0)
    gl.uniform1f(loc.uMaskFeather, this._uniforms.uMaskFeather || 0.1)
    for (const key of Object.keys(IDENTITY)) {
      gl.uniform1f(loc[key], this._uniforms[key])
    }
    gl.drawArrays(gl.TRIANGLES, 0, 3)
  }

  // Refresh the person-confidence mask at the governor-controlled cadence, reusing the
  // previous mask between updates (segmentation is the expensive part of blur). A
  // transient segmentation error keeps the last mask rather than dropping the frame —
  // never a black tile; a fully dead segmenter simply leaves uHasMask 0 (raw background).
  _updateMask(frame) {
    this._frameCount++
    const due = (this._frameCount % this._maskInterval) === 0
    if (!due && this._hasMask) {
      return
    }
    try {
      const mask = this._segmenter.segment(frame)
      if (mask && mask.data && mask.width && mask.height) {
        this._uploadMask(mask)
        this._hasMask = true
      }
    } catch (_) {
      // Keep the last mask; persistent failure just leaves the background unblurred.
    }
  }

  _uploadMask(mask) {
    const gl = this._gl
    if (!gl || !this._maskTexture) {
      return
    }
    gl.activeTexture(gl.TEXTURE1)
    gl.bindTexture(gl.TEXTURE_2D, this._maskTexture)
    gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1)
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.R8, mask.width, mask.height, 0, gl.RED, gl.UNSIGNED_BYTE, mask.data)
    gl.activeTexture(gl.TEXTURE0)
  }

  // --- Tier 1: insertable streams ------------------------------------------

  async _startInsertable(sourceTrack) {
    const processor = new MediaStreamTrackProcessor({ track: sourceTrack })
    const generator = new MediaStreamTrackGenerator({ kind: 'video' })
    this._processor = processor
    this._generator = generator
    this._reader = processor.readable.getReader()
    this._writer = generator.writable.getWriter()
    this._pump()
    return generator
  }

  async _pump() {
    while (this.active) {
      let result
      try {
        result = await this._reader.read()
      } catch (error) {
        // A read rejection means the source frame stream is broken: fail over to the
        // raw camera rather than leaving a frozen last frame on the far end. Guard on
        // this.active so an intentional stop() (which cancels the reader) is silent.
        if (this.active) {
          this._fail(error)
        }
        break
      }
      if (result.done) {
        // A clean end is the REALISTIC failure: when the source track ends (camera
        // unplugged, permission revoked, upstream stop()) the reader resolves
        // { done: true } — it does NOT reject. Without failing over here the generator
        // just freezes its last frame on the far end forever. An intentional stop()
        // sets this.active=false first, so it breaks quietly instead.
        if (this.active) {
          this._fail(new Error('look pipeline source track ended'))
        }
        break
      }
      const frame = result.value
      const t0 = performance.now()
      try {
        // Governor stage 2 passes frames straight through — cheapest possible relief
        // while keeping the SAME output track (never a black tile, never a stall).
        if (this._governorStage >= 2) {
          await this._writer.write(frame)
          this._consecutiveErrors = 0
          this._recordFrameTime(performance.now() - t0)
          continue
        }
        if (this._segmenter) {
          this._updateMask(frame)
        }
        this._draw(frame, frame.displayWidth, frame.displayHeight)
        const out = new VideoFrame(this._canvas, {
          timestamp: frame.timestamp,
          alpha: 'discard'
        })
        frame.close()
        try {
          await this._writer.write(out)
        } catch (writeErr) {
          out.close()
          throw writeErr
        }
        this._consecutiveErrors = 0
        this._recordFrameTime(performance.now() - t0)
      } catch (error) {
        try { frame.close() } catch (_) {}
        this._consecutiveErrors++
        if (this._consecutiveErrors >= CONSECUTIVE_ERROR_LIMIT) {
          this._fail(error)
          break
        }
      }
    }
  }

  // --- Tier 2: canvas.captureStream (Safari desktop) -----------------------

  async _startCanvas(sourceTrack) {
    const video = document.createElement('video')
    video.muted = true
    video.playsInline = true
    video.autoplay = true
    video.srcObject = new MediaStream([sourceTrack])
    this._srcVideo = video
    try {
      await video.play()
    } catch (_) {
      // Autoplay of a muted local stream is normally allowed; if not, the rAF loop
      // still runs and simply draws nothing until frames arrive.
    }
    // Draw once so the captured canvas is a valid frame from the first tick.
    this._draw(video, video.videoWidth || 1280, video.videoHeight || 720)
    const stream = this._canvas.captureStream(30)
    this._capStream = stream
    this._raf = requestAnimationFrame(this._boundRafTick)
    const track = stream.getVideoTracks()[0]
    if (!track) {
      throw new Error('canvas.captureStream produced no video track')
    }
    return track
  }

  _rafTick() {
    if (!this.active) {
      return
    }
    const video = this._srcVideo
    const t0 = performance.now()
    try {
      if (video && video.videoWidth) {
        this._draw(video, video.videoWidth, video.videoHeight)
        this._consecutiveErrors = 0
        this._recordFrameTime(performance.now() - t0)
      }
    } catch (error) {
      this._consecutiveErrors++
      if (this._consecutiveErrors >= CONSECUTIVE_ERROR_LIMIT) {
        this._fail(error)
        return
      }
    }
    this._raf = requestAnimationFrame(this._boundRafTick)
  }

  // --- Thermal governor -----------------------------------------------------

  _readBattery() {
    if (typeof navigator === 'undefined' || typeof navigator.getBattery !== 'function') {
      return
    }
    navigator.getBattery().then(battery => {
      this._battery = battery
      const tighten = () => {
        // A draining, near-empty battery lowers the budget so we shed sooner.
        if (!battery.charging && battery.level <= 0.2) {
          this.frameBudgetMs = Math.min(this.frameBudgetMs, 11)
        }
      }
      tighten()
      battery.addEventListener?.('levelchange', tighten)
      battery.addEventListener?.('chargingchange', tighten)
    }).catch(() => {})
  }

  _recordFrameTime(ms) {
    this._emaMs = this._emaMs ? this._emaMs * 0.9 + ms * 0.1 : ms
    if (this._emaMs > this.frameBudgetMs) {
      this._hotFrames++
      this._coolFrames = 0
      if (this._hotFrames > 75) { // ~2.5s sustained breach at 30fps
        this._hotFrames = 0
        this._escalateGovernor()
      }
      return
    }
    this._hotFrames = Math.max(0, this._hotFrames - 1)
    if (this._governorStage > 0 && this._emaMs < this.frameBudgetMs * 0.6) {
      this._coolFrames++
      if (this._coolFrames * (1000 / 30) >= this.coolMs) {
        this._restoreGovernor()
      }
    }
  }

  // intensity -> look paused -> signal audio, each step honest in the status.
  _escalateGovernor() {
    if (this._governorStage >= 3) {
      return
    }
    this._governorStage++
    if (this._governorStage === 1) {
      if (isSegmentedLook(this.look)) {
        // Blur's cost is segmentation, not grading. Shed THAT first: halve the mask
        // cadence and shrink the blur radius before snapping the whole look to identity —
        // keeping the encoder off the CPU-starvation cliff the 2026-07-06 remote-tile
        // flicker incident traced to per-frame contention.
        this._maskInterval = Math.min(8, this._maskInterval * 2)
        this._uniforms.uBlurRadius = (this._uniforms.uBlurRadius || 0) * 0.5
      } else {
        this._effectiveIntensity = 0 // ease the look off; cheap identity path in-shader
      }
      this._emit('paused-battery')
    } else if (this._governorStage === 2) {
      this._emit('paused-battery') // frames now pass straight through (uBypass)
    } else if (this._governorStage === 3) {
      try { this.onShedAudio() } catch (_) {}
      this._emit('paused-battery')
    }
  }

  _restoreGovernor() {
    if (this._governorStage >= 3) {
      try { this.onRestoreAudio() } catch (_) {}
    }
    this._governorStage = 0
    this._coolFrames = 0
    this._maskInterval = MASK_INTERVAL_FRAMES
    if (isSegmentedLook(this.look)) {
      // Restore the preset blur radius the stage-1 shed halved (blur has no slider
      // overrides, so the preset is the source of truth).
      this._uniforms.uBlurRadius = (VIDEO_LOOK_PRESETS[this.look] || {}).uBlurRadius || 0
    }
    this._effectiveIntensity = this.look === 'none' ? 0 : this.intensity
    this._emit(this.tier === 'none' ? 'preview-only' : 'active')
  }

  // --- teardown / status ----------------------------------------------------

  _fail(error) {
    if (this._settled) {
      return
    }
    this._settled = true
    this.active = false
    this._teardownGraph()
    this._emit('error')
    try { this.onError(error) } catch (_) {}
  }

  _teardownGraph() {
    if (this._raf) {
      cancelAnimationFrame(this._raf)
      this._raf = 0
    }
    if (this._segmenter) {
      try { this._segmenter.close() } catch (_) {}
      this._segmenter = null
    }
    this._hasMask = false
    this._frameCount = 0
    try { this._reader?.cancel() } catch (_) {}
    try { this._writer?.close() } catch (_) {}
    this._reader = null
    this._writer = null
    this._processor = null
    if (this._generator) {
      try { this._generator.stop?.() } catch (_) {}
      this._generator = null
    }
    if (this._capStream) {
      this._capStream.getTracks().forEach(track => { try { track.stop() } catch (_) {} })
      this._capStream = null
    }
    if (this._srcVideo) {
      try { this._srcVideo.pause() } catch (_) {}
      this._srcVideo.srcObject = null
      this._srcVideo = null
    }
    if (this._gl) {
      const lose = this._gl.getExtension('WEBGL_lose_context')
      try { lose?.loseContext() } catch (_) {}
    }
    this._gl = null
    this._canvas = null
    this._program = null
    this._texture = null
    this._maskTexture = null
    this._outputTrack = null
  }

  _emit(state) {
    let text = ''
    switch (state) {
      case 'active': text = 'Active — far end sees it'; break
      case 'preview-only': text = 'Preview only — not supported on this browser'; break
      case 'paused-battery': text = 'Paused to save battery'; break
      case 'error': text = 'Off — look unavailable, using raw camera'; break
      case 'off':
      default: text = 'Off'
    }
    try {
      this.onStatus({ state, text, tier: this.tier, look: this.look, governorStage: this._governorStage })
    } catch (_) {}
  }
}

// CSS-filter approximation of each look for the LOCAL PREVIEW ONLY (tier 'none' /
// honest fallback). Intensity scales the filter toward identity. This never claims
// the far end sees it — it is only ever applied to a local <video>.
export function cssFilterForLook(look, intensity = 1) {
  const t = Math.max(0, Math.min(1, intensity))
  const lerp = (a, b) => (a + (b - a) * t).toFixed(3)
  switch (look) {
    case 'bonfire-warm':
      return `saturate(${lerp(1, 1.08)}) contrast(${lerp(1, 1.05)}) brightness(${lerp(1, 1.03)}) sepia(${lerp(0, 0.12)})`
    case 'studio':
      return `contrast(${lerp(1, 1.12)}) saturate(${lerp(1, 1.02)}) brightness(${lerp(1, 1.02)})`
    case 'mono':
      return `grayscale(${lerp(0, 1)}) contrast(${lerp(1, 1.15)})`
    case 'lowlight':
      return `brightness(${lerp(1, 1.35)}) contrast(${lerp(1, 0.94)}) saturate(${lerp(1, 1.04)})`
    case 'blur':
      // No honest CSS approximation: a CSS blur() defocuses the WHOLE frame including the
      // subject, not just the background. Where the real (segmented) pipeline can't run,
      // the preview shows the raw camera — this 'none' — matching the honest status.
      return 'none'
    case 'none':
    default:
      return 'none'
  }
}
