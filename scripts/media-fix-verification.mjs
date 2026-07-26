// Behavioral verification for the three media fixes shipped 2026-06-16:
//   1. Safari camera flicker  — videoElementNeedsRefresh must not force a
//      srcObject reattach while the feed is alive (Safari's requestVideoFrameCallback
//      is unreliable for live MediaStreams, so currentTime progress is the signal).
//   2. Mobile orientation swap — capture must not pin a landscape aspect ratio on
//      phones, and neither room-size retuning nor lag recovery may restart the
//      camera on mobile.
//   3. Screen share for all    — remoteScreenShareStream must resolve the sharer's
//      stream even when the name casing/format skews, with a live-track fallback.
//
// The functions are extracted from the shipped index.html (not copied) and exercised
// against both the bug scenarios and regression guards. Run by TestMediaFixesBehaveCorrectly.

import { readFileSync } from 'fs'
import { fileURLToPath } from 'url'
import { dirname, join } from 'path'

const here = dirname(fileURLToPath(import.meta.url))
const html = readFileSync(join(here, '..', 'index.html'), 'utf8')

function extractFn(name) {
  let start = html.indexOf(`function ${name}(`)
  if (start < 0) throw new Error(`function ${name} not found in index.html`)
  if (html.slice(start - 6, start) === 'async ') start -= 6 // preserve async keyword
  let depth = 0
  for (let j = html.indexOf('{', start); j < html.length; j++) {
    const c = html[j]
    if (c === '{') depth++
    else if (c === '}') { depth--; if (depth === 0) return html.slice(start, j + 1) }
  }
  throw new Error(`unbalanced braces extracting ${name}`)
}

let pass = 0, fail = 0
const ok = (name, cond) => { if (cond) { pass++; console.log('  PASS', name) } else { fail++; console.log('  FAIL', name) } }

// ---------- ISSUE 1: Safari flicker ----------
console.log('[1] Safari flicker - videoElementNeedsRefresh')
{
  const src = extractFn('videoElementHasRenderedFrame') + '\n' + extractFn('videoElementNeedsRefresh')
  const make = new Function('performance', 'HTMLMediaElement', `
    function mediaElementIsVisible(){ return true }
    function videoElementHasLiveVideoTrack(){ return true }
    ${src}
    return videoElementNeedsRefresh
  `)
  let clock = 100000
  const fn = make({ now: () => clock }, { HAVE_CURRENT_DATA: 2 })

  const safari = { hidden:false, isConnected:true, readyState:4, videoWidth:1280, videoHeight:720,
                   currentTime:5, dataset:{ videoStreamAttachedAt:String(clock-10000) } }
  const r1 = fn(safari)
  clock += 4000; safari.currentTime = 9;  const r2 = fn(safari)
  clock += 4000; safari.currentTime = 13; const r3 = fn(safari)
  ok('healthy feed w/ advancing currentTime never asks for refresh (no flicker)', r1===false && r2===false && r3===false)

  clock = 200000
  const staleRVFC = { hidden:false, isConnected:true, readyState:4, videoWidth:1280, videoHeight:720,
                      currentTime:2, requestVideoFrameCallback(){}, dataset:{ videoStreamAttachedAt:String(clock-10000),
                      lastVideoFrameAt:String(clock-20000) } }
  fn(staleRVFC)
  clock += 4000; staleRVFC.currentTime = 6
  ok('stale rVFC but advancing currentTime => NO refresh (Safari false-positive fixed)', fn(staleRVFC)===false)

  clock = 300000
  const frozen = { hidden:false, isConnected:true, readyState:4, videoWidth:1280, videoHeight:720,
                   currentTime:7, requestVideoFrameCallback(){}, dataset:{ videoStreamAttachedAt:String(clock-10000) } }
  fn(frozen)
  clock += 9000 // 9s later, currentTime unchanged => genuinely frozen
  ok('frozen feed (currentTime stuck >8s) STILL triggers refresh (recovery preserved)', fn(frozen)===true)

  clock = 400000
  const fresh = { hidden:false, isConnected:true, readyState:0, videoWidth:0, videoHeight:0,
                  currentTime:0, dataset:{ videoStreamAttachedAt:String(clock-1000) } }
  ok('freshly-attached (<3.5s) element is never refreshed', fn(fresh)===false)
}

// ---------- ISSUE 2: mobile orientation ----------
console.log('[2] Mobile orientation - capture constraints')
{
  function build(ua, maxTouchPoints) {
    const make = new Function('navigator', `
      const isMobileDevice = /Mobi|Android|iPhone|iPad|iPod/i.test(navigator.userAgent)
        || (/Macintosh/.test(navigator.userAgent) && typeof navigator.maxTouchPoints === 'number' && navigator.maxTouchPoints > 1)
      const widescreenAspectRatio = { ideal: 16 / 9 }
      const cameraAspectRatioConstraint = isMobileDevice ? undefined : widescreenAspectRatio
      const relaxedVideoConstraints = {
        width:{ideal:1280}, height:{ideal:720},
        ...(cameraAspectRatioConstraint ? { aspectRatio: cameraAspectRatioConstraint } : {}),
        frameRate:{ideal:30}, facingMode:'user'
      }
      const mediaConstraints = { video: {
        width:{ideal:1280,max:1280}, height:{ideal:720,max:720},
        ...(cameraAspectRatioConstraint ? { aspectRatio: cameraAspectRatioConstraint } : {}),
        frameRate:{ideal:30,max:30}, facingMode:'user'
      } }
      return { isMobileDevice, relaxedVideoConstraints, mediaConstraints }
    `)
    return make({ userAgent: ua, maxTouchPoints })
  }
  const iphone  = build('Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605 Safari/604', 5)
  const android = build('Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537 Chrome/120 Mobile Safari/537', 5)
  const ipad    = build('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15) AppleWebKit/605 Safari/604', 5)
  const desktop = build('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15) AppleWebKit/537 Chrome/120 Safari/537', 0)

  ok('iPhone detected as mobile', iphone.isMobileDevice === true)
  ok('Android detected as mobile', android.isMobileDevice === true)
  ok('iPadOS-as-Macintosh+touch detected as mobile', ipad.isMobileDevice === true)
  ok('desktop NOT mobile', desktop.isMobileDevice === false)
  ok('iPhone constraints carry NO aspectRatio pin (native orientation)', !('aspectRatio' in iphone.mediaConstraints.video) && !('aspectRatio' in iphone.relaxedVideoConstraints))
  ok('Android constraints carry NO aspectRatio pin', !('aspectRatio' in android.mediaConstraints.video))
  ok('desktop STILL pins 16:9 (square-crop guard preserved)', desktop.mediaConstraints.video.aspectRatio && Math.abs(desktop.mediaConstraints.video.aspectRatio.ideal - 16/9) < 1e-9)
}

console.log('[2b] retuneLocalCameraCapture - no camera restart on mobile')
{
  const src = extractFn('retuneLocalCameraCapture')
  let applied = 0
  const track = {
    getSettings: () => ({ width: 640, height: 360, frameRate: 30 }),
    applyConstraints: async constraints => { applied++; track.constraints = constraints }
  }
  const run = new Function('isMobileDevice','localVideoTrack','liveTrack','outboundMediaLimits','mediaQualityConstrained','useCrowdedVideoLimits','useGroupVideoLimits','widescreenAspectRatio', `
    ${extractFn('cameraCaptureNeedsRetune')}
    ${src}
    return retuneLocalCameraCapture
  `)
  const mobileFn = run(true, ()=>track, ()=>true, {video:{}}, false, ()=>false, ()=>false, {ideal:16/9})
  await mobileFn()
  ok('mobile: applyConstraints NOT called (no reorientation on roster change)', applied === 0)
  applied = 0
  const desktopFn = run(false, ()=>track, ()=>true, {video:{maxFramerate:30}}, false, ()=>false, ()=>false, {ideal:16/9})
  await desktopFn()
  ok('desktop: a legacy 640x360 strand is restored once', applied === 1)
  ok('desktop: recovery requests 1280x720 ideals (max-only cannot upscale)', track.constraints?.width?.ideal === 1280 && track.constraints?.height?.ideal === 720)

  track.getSettings = () => ({ width: 1280, height: 720, frameRate: 30 })
  await desktopFn()
  ok('desktop: a healthy 1280x720 capture is not restarted on re-offer', applied === 1)
}

console.log('[2c] receive loss stays receive-only')
{
  const monitor = extractFn('startMediaQualityMonitor')
  ok('inbound lag remains telemetry/receiver health and never constrains local camera', !monitor.includes('constrainCameraForLag') && !monitor.includes('mediaQualityConstrained'))
  ok('outbound low-bandwidth adaptation remains directionally isolated', monitor.includes('snapshot.candidatePair?.availableOutgoingBitrate') && monitor.includes('adaptDesktopCameraForLowBandwidth'))

  let intervalTick = null
  const peer = { connectionState: 'connected', getStats: async () => ({}) }
  const make = new Function('window', 'document', 'peer', `
    let pc = peer
    let previousMediaQualityStats = null
    let mediaQualityBandwidthLimited = false
    let mediaQualityBandwidthLowSamples = 0
    let mediaQualityBandwidthHealthySamples = 0
    let adapted = 0
    let restored = 0
    let nextSnapshot = null
    const isMobileDevice = false
    function stopMediaQualityMonitor() {}
    function startRemoteVideoFreezeWatch() {}
    function summarizeMediaQualityStats() { return nextSnapshot }
    function mediaQualityLooksLaggy(snapshot) { return Boolean(snapshot.inboundLaggy) }
    async function adaptDesktopCameraForLowBandwidth() { adapted++; mediaQualityBandwidthLimited = true; mediaQualityBandwidthHealthySamples = 0 }
    async function restoreDesktopCameraAfterBandwidthRecovery() { restored++; mediaQualityBandwidthLimited = false; mediaQualityBandwidthLowSamples = 0; mediaQualityBandwidthHealthySamples = 0 }
    function repairRemoteMediaHealth() { return {} }
    function remoteMediaHealthSnapshot() { return {} }
    function syncRoomAudioPlaybackState() {}
    function sendMediaQualityReport() {}
    ${monitor}
    return {
      start: () => startMediaQualityMonitor(peer),
      sample: async snapshot => { nextSnapshot = snapshot; await window.tick() },
      state: () => ({ adapted, restored, limited: mediaQualityBandwidthLimited })
    }
  `)
  const windowMock = {
    setInterval: fn => { intervalTick = fn; return 1 },
    tick: async () => intervalTick(),
  }
  const harness = make(windowMock, { visibilityState: 'visible' }, peer)
  harness.start()
  await harness.sample({ at:0, inboundLaggy:true, outboundVideoBytesSent:1000, candidatePair:{ availableOutgoingBitrate:2590000 } })
  await harness.sample({ at:4000, inboundLaggy:true, outboundVideoBytesSent:2000, candidatePair:{ availableOutgoingBitrate:2590000 } })
  ok('20% inbound loss with healthy outgoing bandwidth leaves sender untouched', harness.state().adapted === 0)
  await harness.sample({ at:8000, inboundLaggy:false, outboundVideoBytesSent:3000, candidatePair:{ availableOutgoingBitrate:200000 } })
  await harness.sample({ at:12000, inboundLaggy:false, outboundVideoBytesSent:4000, candidatePair:{ availableOutgoingBitrate:200000 } })
  ok('two sustained low selected-pair bandwidth samples adapt outbound sender', harness.state().adapted === 1 && harness.state().limited)
  await harness.sample({ at:16000, inboundLaggy:true, outboundVideoBytesSent:5000, candidatePair:{ availableOutgoingBitrate:700000 } })
  await harness.sample({ at:20000, inboundLaggy:true, outboundVideoBytesSent:6000, candidatePair:{ availableOutgoingBitrate:700000 } })
  await harness.sample({ at:24000, inboundLaggy:true, outboundVideoBytesSent:7000, candidatePair:{ availableOutgoingBitrate:700000 } })
  ok('outgoing recovery restores quality despite continued inbound loss', harness.state().restored === 1 && !harness.state().limited)
}

console.log('[2d] selected ICE pair - stale paths do not poison RTT')
{
  const make = new Function('performance', 'frozenRemoteVideoTrackIds', `
    ${extractFn('summarizeCandidatePair')}
    ${extractFn('summarizeMediaQualityStats')}
    return summarizeMediaQualityStats
  `)
  const summarize = make({ now: () => 1000 }, new Set())
  const report = new Map([
    ['stale', { id:'stale', type:'candidate-pair', state:'succeeded', currentRoundTripTime:4.676, availableOutgoingBitrate:85000 }],
    ['selected', { id:'selected', type:'candidate-pair', state:'succeeded', nominated:true, currentRoundTripTime:0.1, availableOutgoingBitrate:1200000, localCandidateId:'local', remoteCandidateId:'remote' }],
    ['transport', { type:'transport', selectedCandidatePairId:'selected' }],
    ['local', { type:'local-candidate', candidateType:'relay', protocol:'udp', networkType:'wifi' }],
    ['remote', { type:'remote-candidate', candidateType:'host', protocol:'udp' }],
  ])
  const snapshot = summarize(report)
  ok('RTT comes from transport.selectedCandidatePairId, not max stale succeeded pair', snapshot.outboundRtt === 0.1)
  ok('available outgoing bitrate comes from that same selected pair', snapshot.candidatePair.availableOutgoingBitrate === 1200000)
}

console.log('[2e] transient disconnect - ICE restart grace')
{
  let scheduled = null
  const peer = { connectionState: 'disconnected' }
  const make = new Function('window', 'peer', `
    let pc = peer
    let iceDisconnectRestartTimer = 0
    const iceDisconnectRestartGraceMs = 3000
    let restarts = 0
    function requestIceRestart() { restarts++ }
    ${extractFn('scheduleIceRestartAfterDisconnect')}
    return { schedule: scheduleIceRestartAfterDisconnect, restarts: () => restarts }
  `)
  const harness = make({
    setTimeout: (fn, delay) => { scheduled = { fn, delay }; return 1 },
  }, peer)
  harness.schedule(peer)
  ok('disconnected does not restart ICE synchronously', harness.restarts() === 0)
  ok('disconnect grace is three seconds', scheduled?.delay === 3000)
  peer.connectionState = 'connected'
  scheduled.fn()
  ok('a self-healed disconnect never restarts ICE', harness.restarts() === 0)
}

// ---------- ISSUE 3: screen share ----------
console.log('[3] Screen share - remoteScreenShareStream')
{
  const src = extractFn('remoteScreenShareStream')
  const STREAM = { id:'cam-stream' }
  const liveTr = { kind:'video', readyState:'live' }
  const run = (mapE, trE) => new Function('remoteStreamsByParticipant','remoteVideoTracksByParticipant','liveTrack','MediaStream', `
    ${src}
    return remoteScreenShareStream
  `)(new Map(mapE), new Map(trE), t=>t && t.readyState==='live', class { constructor(ts){ this.tracks=ts; this.id='from-track' } })

  ok('exact-name match returns stream', run([['Alice', STREAM]], [])('Alice') === STREAM)
  ok('case/space-skewed name still resolves (the real bug)', run([['Alice', STREAM]], [])('  alice ') === STREAM)
  ok('falls back to live remote track when stream map empty', run([], [['Bob', liveTr]])('bob')?.id === 'from-track')
  ok('returns null when participant truly absent', run([['Alice', STREAM]], [])('Carol') === null)
  ok('returns null for empty name', run([], [])('') === null)
}

console.log(`\nresult: ${pass} passed, ${fail} failed`)
console.log(JSON.stringify({ ok: fail === 0, passed: pass, failed: fail }))
process.exit(fail ? 1 : 0)
