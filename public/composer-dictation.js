/* STRIDE composer dictation: bounded recording is local until an explicit Send.
 * This file deliberately contains no provider/model configuration. The app
 * injects an authenticated server adapter, keeping credentials server-side. */
(function (root) {
  'use strict'

  function nextDictationState(state, event) {
    const table = {
      idle: { record: 'recording' },
      recording: { stop: 'held', cancel: 'idle' },
      held: { send: 'transcribing', delete: 'idle' },
      transcribing: { complete: 'idle', fail: 'held', delete: 'idle' }
    }
    return table[state]?.[event] || state
  }

  function dictationIcon(kind) {
    const paths = {
      mic: '<rect x="9" y="2.5" width="6" height="12" rx="3"></rect><path d="M5.5 11.5a6.5 6.5 0 0 0 13 0M12 18v3M8.5 21h7"></path>',
      stop: '<rect x="7" y="7" width="10" height="10" rx="2"></rect>',
      delete: '<path d="M6 6l12 12M18 6 6 18"></path>',
      send: '<path d="M12 19V5M6.5 10.5 12 5l5.5 5.5"></path>'
    }
    return `<svg class="stride-dictation-icon" viewBox="0 0 24 24" aria-hidden="true">${paths[kind] || paths.mic}</svg>`
  }

  class AudioFocusCoordinator {
    constructor() {
      this.active = null
      this.generation = 0
      this.intentGeneration = 0
      this.latestIntent = null
      this.queue = Promise.resolve()
    }
    acquire(mode, hooks = {}) {
      const active = { mode, generation: ++this.generation, close: hooks.close, closePromise: null }
      const intent = {
        id: ++this.intentGeneration,
        owner: active,
        reason: `superseded_by_${mode}`
      }
      // Linearization point: the old lease is stale synchronously, before its
      // potentially slow close hook. Only this latest intent may be granted.
      this.latestIntent = intent
      const lease = this.leaseFor(active)
      return this.enqueue(async () => {
        try {
          if (this.latestIntent !== intent) {
            await this.close(active, this.latestSupersessionReason(mode))
            return lease
          }
          const prior = this.active
          this.active = null
          if (prior) await this.close(prior, `superseded_by_${mode}`)
          if (this.latestIntent !== intent) {
            await this.close(active, this.latestSupersessionReason(mode))
            return lease
          }
          this.active = active
          return lease
        } catch (error) {
          if (this.latestIntent === intent) {
            this.latestIntent = {
              id: ++this.intentGeneration,
              owner: null,
              reason: 'error'
            }
            this.generation += 1
          }
          try { await this.close(active, 'error') } catch { /* Preserve the transition error. */ }
          throw error
        }
      })
    }
    leaseFor(active) {
      return {
        generation: active.generation,
        isCurrent: () => this.active === active && this.latestIntent?.owner === active,
        release: (reason = 'completed') => {
          if (this.active !== active || this.latestIntent?.owner !== active) return Promise.resolve(false)
          const intent = {
            id: ++this.intentGeneration,
            owner: null,
            reason
          }
          this.latestIntent = intent
          this.generation += 1
          return this.enqueue(async () => {
            if (this.active !== active) return false
            this.active = null
            await this.close(active, reason)
            return true
          })
        }
      }
    }
    enqueue(work) {
      const operation = this.queue.then(work)
      this.queue = operation.then(() => undefined, () => undefined)
      return operation
    }
    latestSupersessionReason(fallbackMode) {
      return this.latestIntent?.owner
        ? `superseded_by_${this.latestIntent.owner.mode}`
        : this.latestIntent?.reason || `superseded_by_${fallbackMode}`
    }
    close(active, reason) {
      if (!active.closePromise) active.closePromise = Promise.resolve().then(() => active.close?.(reason))
      return active.closePromise.finally(() => {
        if (this.active === active) this.active = null
      })
    }
  }

  class ComposerDictationController {
    constructor(options) {
      this.form = options.form
      this.input = options.input
      this.context = options.context || 'chat'
      this.focus = options.focus
      this.beforeCapture = options.beforeCapture || (async () => null)
      this.afterCapture = options.afterCapture || (async () => {})
      this.transcribe = options.transcribe
      this.state = 'idle'
      this.generation = 0
      this.stream = null
      this.recorder = null
      this.chunks = []
      this.startedAt = 0
      this.restore = null
      this.lease = null
      this.startAttempt = null
      this.stopPromise = null
      this.submittedGeneration = 0
      this.errorMessage = ''
      this.stopTimeoutMs = 2000
      this.install()
    }
    install() {
      if (!document.getElementById('stride-dictation-styles')) {
        const styles = document.createElement('style')
        styles.id = 'stride-dictation-styles'
        styles.textContent = `
          .stride-dictation-composer { position: relative; }
          .stride-dictation-composer--nested { display: flex; align-items: flex-end; gap: 6px; width: 100%; min-width: 0; }
          .stride-dictation-composer--nested > .stride-dictation-input { flex: 1 1 auto; min-width: 0; }
          .stride-dictation-mic, .stride-dictation-action { display: inline-flex; align-items: center; justify-content: center; flex: 0 0 auto; min-width: 34px; height: 34px; border: 0; border-radius: 999px; background: transparent; color: inherit; font: 600 11px/1 system-ui; cursor: pointer; }
          .stride-dictation-action[hidden], .stride-dictation-wave[hidden] { display: none !important; }
          .stride-dictation-mic:hover, .stride-dictation-action:hover { background: rgba(255,255,255,.09); }
          .stride-dictation-icon { width: 18px; height: 18px; fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
          .stride-dictation-composer:not([data-dictation-state="idle"]) > :not(.stride-dictation-mic):not(.stride-dictation-status):not(.stride-dictation-wave):not(.stride-dictation-action) { display: none !important; }
          .stride-dictation-composer[data-dictation-state="held"] > .stride-dictation-mic,
          .stride-dictation-composer[data-dictation-state="transcribing"] > .stride-dictation-mic { display: none; }
          .stride-dictation-composer[data-dictation-state="recording"] { box-shadow: 0 0 0 1px rgba(255,91,25,.65), 0 0 22px rgba(255,91,25,.18); }
          .stride-dictation-status { min-width: 0; font: 600 11px/1 system-ui; white-space: nowrap; color: var(--ink-muted,#8b8b90); }
          .stride-dictation-composer[data-dictation-state="held"] > .stride-dictation-delete,
          .stride-dictation-composer[data-dictation-state="transcribing"] > .stride-dictation-delete { order: -2; }
          .stride-dictation-composer[data-dictation-state="held"] > .stride-dictation-status,
          .stride-dictation-composer[data-dictation-state="transcribing"] > .stride-dictation-status { order: -1; }
          .stride-dictation-composer[data-dictation-state="transcribing"] .stride-dictation-status { color: var(--accent,#ff5a19); }
          .stride-dictation-wave { flex: 1 1 44px; min-width: 20px; width: 44px; height: 14px; background: repeating-linear-gradient(90deg,currentColor 0 2px,transparent 2px 5px); mask: linear-gradient(90deg,transparent,currentColor 18%,currentColor 82%,transparent); -webkit-mask: linear-gradient(90deg,transparent,currentColor 18%,currentColor 82%,transparent); opacity: .75; }
          .stride-dictation-composer[data-dictation-state="recording"] .stride-dictation-wave { animation: stride-dictation-wave .8s ease-in-out infinite alternate; }
          @keyframes stride-dictation-wave { to { transform: scaleY(1.55); opacity: 1; } }
        `
        document.head.appendChild(styles)
      }
      this.form.classList.add('stride-dictation-form')
      this.input.classList.add('stride-dictation-input')
      if (this.input.parentElement === this.form) {
        this.host = this.form
      } else {
        this.host = document.createElement('span')
        this.host.className = 'stride-dictation-composer stride-dictation-composer--nested'
        this.input.parentNode.insertBefore(this.host, this.input)
        this.host.appendChild(this.input)
      }
      this.host.classList.add('stride-dictation-composer')
      this.button = document.createElement('button')
      this.button.type = 'button'
      this.button.className = 'stride-dictation-mic'
      this.button.setAttribute('aria-label', 'Dictate message')
      this.button.title = 'Dictate'
      this.button.innerHTML = dictationIcon('mic')
      this.status = document.createElement('span')
      this.status.className = 'stride-dictation-status'
      this.status.setAttribute('aria-live', 'polite')
      this.wave = document.createElement('span')
      this.wave.className = 'stride-dictation-wave'
      this.wave.setAttribute('aria-hidden', 'true')
      this.deleteButton = document.createElement('button')
      this.deleteButton.type = 'button'
      this.deleteButton.className = 'stride-dictation-action stride-dictation-delete'
      this.deleteButton.innerHTML = dictationIcon('delete')
      this.deleteButton.setAttribute('aria-label', 'Delete dictated clip')
      this.deleteButton.title = 'Delete recording'
      this.sendButton = document.createElement('button')
      this.sendButton.type = 'button'
      this.sendButton.className = 'stride-dictation-action stride-dictation-send'
      this.sendButton.innerHTML = dictationIcon('send')
      this.sendButton.setAttribute('aria-label', 'Transcribe and send dictated clip')
      this.sendButton.title = 'Transcribe and send'
      this.button.addEventListener('click', () => this.state === 'recording' ? this.stop() : this.start())
      this.deleteButton.addEventListener('click', () => this.discard())
      this.sendButton.addEventListener('click', () => this.send())
      this.host.insertBefore(this.button, this.input.nextSibling)
      this.host.insertBefore(this.status, this.button.nextSibling)
      this.host.insertBefore(this.wave, this.status.nextSibling)
      this.host.insertBefore(this.deleteButton, this.wave.nextSibling)
      this.host.insertBefore(this.sendButton, this.deleteButton.nextSibling)
      new MutationObserver(() => this.render()).observe(this.input, { attributes: true, attributeFilter: ['disabled'] })
      this.render()
    }
    render() {
      const state = this.state
      this.host.dataset.dictationState = state
      this.button.disabled = Boolean(this.input.disabled) || state === 'held' || state === 'transcribing'
      this.button.innerHTML = dictationIcon(state === 'recording' ? 'stop' : 'mic')
      this.button.setAttribute('aria-label', state === 'recording' ? 'Stop dictation recording' : 'Dictate message')
      this.button.title = state === 'recording' ? 'Stop recording' : 'Dictate'
      this.status.textContent = state === 'recording'
        ? 'Recording'
        : state === 'held'
          ? this.errorMessage || 'Ready to transcribe'
          : state === 'transcribing'
            ? 'Transcribing'
            : this.errorMessage
      this.wave.hidden = state !== 'recording' && state !== 'held'
      this.deleteButton.hidden = state !== 'held' && state !== 'transcribing'
      this.sendButton.hidden = state !== 'held'
    }
    async start() {
      if (this.state !== 'idle' || this.startAttempt || this.input.disabled || !navigator.mediaDevices?.getUserMedia || !root.MediaRecorder) return
      let settleAttempt
      const attempt = {
        lease: null,
        cancelled: false,
        settled: new Promise((resolve) => { settleAttempt = resolve })
      }
      // Cancellation must exist before focus acquisition. Room entry, discard,
      // or teardown can otherwise return while this await is still pending and
      // allow a late microphone capture to start behind the new surface.
      this.startAttempt = attempt
      this.errorMessage = ''
      let lease = null
      let terminalReason = ''
      let terminalError = null
      try {
        lease = await this.focus.acquire('composer_dictation', { close: () => this.park('focus_takeover') })
        attempt.lease = lease
        this.lease = lease
        if (attempt.cancelled || !lease.isCurrent()) {
          terminalReason = 'cancelled'
        } else {
          this.restore = await this.beforeCapture()
          if (attempt.cancelled || !lease.isCurrent()) {
            terminalReason = 'cancelled'
          } else {
            this.stream = await navigator.mediaDevices.getUserMedia({ audio: true })
            if (attempt.cancelled || !lease.isCurrent()) {
              terminalReason = 'cancelled'
            } else {
              const AudioContext = root.AudioContext || root.webkitAudioContext
              if (AudioContext) {
                this.audioContext = new AudioContext()
                this.analyser = this.audioContext.createAnalyser()
                this.analyser.fftSize = 256
                this.audioContext.createMediaStreamSource(this.stream).connect(this.analyser)
                const samples = new Uint8Array(this.analyser.fftSize)
                const meter = () => {
                  if (this.state !== 'recording' || !this.analyser) return
                  this.analyser.getByteTimeDomainData(samples)
                  let sum = 0
                  for (const sample of samples) { const value = (sample - 128) / 128; sum += value * value }
                  const level = Math.min(1, Math.sqrt(sum / samples.length) * 4)
                  this.wave.style.transform = `scaleY(${0.45 + level})`
                  this.meterFrame = root.requestAnimationFrame(meter)
                }
                this.meterFrame = root.requestAnimationFrame(meter)
              }
              this.chunks = []
              this.startedAt = Date.now()
              this.recorder = new root.MediaRecorder(this.stream, { mimeType: root.MediaRecorder.isTypeSupported?.('audio/webm') ? 'audio/webm' : undefined })
              this.recorder.addEventListener('dataavailable', (event) => { if (event.data?.size) this.chunks.push(event.data) })
              this.recorder.start(250)
              this.state = nextDictationState(this.state, 'record')
              this.render()
            }
          }
        }
      } catch (error) {
        terminalReason = 'error'
        terminalError = error
      } finally {
        let cleanupError = null
        if (terminalReason) {
          try { await this.cleanupCapture() } catch (error) { cleanupError = error }
          this.state = 'idle'
          if (this.lease === lease) this.lease = null
          this.lastTerminalReason = attempt.cancelled ? 'focus_takeover' : terminalReason === 'error' ? 'error' : 'stale_generation'
          this.errorMessage = terminalReason === 'error' || cleanupError
            ? 'Could not start dictation. Tap the mic to try again.'
            : ''
          try { this.render() } catch (error) { cleanupError ||= error }
        }
        // Settle only after media and room-mute restoration, but before lease
        // release. Releasing a current lease calls its close hook, which drains
        // this same attempt; reversing these two steps self-deadlocks.
        if (this.startAttempt === attempt) this.startAttempt = null
        settleAttempt()
        if (terminalReason && lease) {
          try { await lease.release(terminalReason) } catch (error) { cleanupError ||= error }
        }
        terminalError ||= cleanupError
      }
      if (terminalError) throw terminalError
    }
    stop() {
      if (this.stopPromise) return this.stopPromise
      if (this.state !== 'recording') return
      const operation = this.finishStop()
      this.stopPromise = operation
      return operation.finally(() => {
        if (this.stopPromise === operation) this.stopPromise = null
      })
    }
    async finishStop() {
      const recorder = this.recorder
      const lease = this.lease
      let stopError = null
      let cleanupError = null
      try {
        if (recorder && recorder.state !== 'inactive') {
          await new Promise((resolve, reject) => {
            let settled = false
            let timeout = null
            const finish = (error) => {
              if (settled) return
              settled = true
              if (timeout !== null) root.clearTimeout?.(timeout)
              if (error) reject(error)
              else resolve()
            }
            recorder.addEventListener('stop', () => finish(), { once: true })
            timeout = root.setTimeout?.(
              () => finish(new Error('MediaRecorder did not finish stopping.')),
              this.stopTimeoutMs,
            ) ?? null
            try { recorder.stop() } catch (error) { finish(error) }
          })
        }
      } catch (error) {
        stopError = error
      } finally {
        try { await this.cleanupCapture() } catch (error) { cleanupError = error }
      }
      this.clip = new Blob(this.chunks, { type: recorder?.mimeType || 'audio/webm' })
      this.durationMs = Math.max(0, Date.now() - this.startedAt)
      const retained = this.clip.size > 0
      this.state = retained ? nextDictationState(this.state, 'stop') : 'idle'
      this.errorMessage = stopError || cleanupError
        ? retained
          ? 'Recording stopped unexpectedly. Your clip is saved; try sending it.'
          : 'Recording stopped unexpectedly. Tap the mic to try again.'
        : ''
      try { this.render() } catch (error) { cleanupError ||= error }
      // The state must no longer say recording before release invokes this
      // owner's close hook; otherwise park() re-enters the in-flight Stop and
      // both promises wait on each other.
      if (this.lease === lease) this.lease = null
      try { await lease?.release(stopError || cleanupError ? 'error' : 'completed') } catch (error) { cleanupError ||= error }
      if (cleanupError && !this.errorMessage) {
        this.errorMessage = retained
          ? 'Recording stopped unexpectedly. Your clip is saved; try sending it.'
          : 'Recording stopped unexpectedly. Tap the mic to try again.'
        try { this.render() } catch { /* Media and focus are already drained. */ }
      }
    }
    async park(reason) {
      const attempt = this.startAttempt
      if (attempt) {
        attempt.cancelled = true
        await attempt.settled
      }
      if (this.state === 'recording') await this.stop()
      // A held clip is intentionally retained; joining a room must not make a
      // provider call or silently discard what the person just said.
      this.lastTerminalReason = reason
    }
    async discard() {
      this.generation += 1
      const attempt = this.startAttempt
      if (attempt) {
        attempt.cancelled = true
        await attempt.settled
      }
      if (this.state === 'recording') await this.stop()
      this.clip = null
      this.chunks = []
      this.state = 'idle'
      this.errorMessage = ''
      this.render()
    }
    async send() {
      if (this.state !== 'held' || !this.clip || !this.transcribe) return
      const generation = ++this.generation
      this.state = nextDictationState(this.state, 'send')
      this.render()
      try {
        const text = String(await this.transcribe(this.clip, { context: this.context, durationMs: this.durationMs }) || '').trim()
        if (generation !== this.generation || this.state !== 'transcribing') return
        if (!text) {
          this.state = nextDictationState(this.state, 'fail')
          this.errorMessage = 'No speech was detected. Your recording is saved; try again or delete it.'
          this.render()
          return
        }
        this.clip = null
        this.state = nextDictationState(this.state, 'complete')
        this.errorMessage = ''
        this.input.value = text
        this.render()
        if (text && this.submittedGeneration !== generation) {
          this.submittedGeneration = generation
          this.form.requestSubmit()
        }
      } catch (error) {
        if (generation !== this.generation) return
        this.state = nextDictationState(this.state, 'fail')
        this.errorMessage = 'Could not transcribe that. Your recording is saved; try again.'
        this.render()
      }
    }
    async cleanupCapture() {
      const failures = []
      this.recorder = null
      if (this.meterFrame) root.cancelAnimationFrame?.(this.meterFrame)
      this.meterFrame = 0
      this.analyser = null
      try { await this.audioContext?.close?.() } catch (error) { failures.push(error) }
      this.audioContext = null
      for (const track of this.stream?.getTracks?.() || []) {
        try { track.stop() } catch (error) { failures.push(error) }
      }
      this.stream = null
      const restore = this.restore
      this.restore = null
      try { await this.afterCapture(restore) } catch (error) { failures.push(error) }
      if (failures.length) throw failures[0]
    }
  }

  root.StrideComposerDictation = { AudioFocusCoordinator, ComposerDictationController, nextDictationState }
  if (typeof module !== 'undefined') module.exports = { AudioFocusCoordinator, ComposerDictationController, nextDictationState }
})(typeof window !== 'undefined' ? window : globalThis)
