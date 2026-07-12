import assert from 'node:assert/strict'
import test from 'node:test'

import {
  validatePinnedViewSnapshot,
  validateScreenShareSnapshot,
  validateSoakProgressSnapshots
} from './live-media-smoke-assertions.mjs'

const visibleRect = { clientRects: 1, rect: { width: 320, height: 180 } }
const hiddenRect = { clientRects: 0, rect: { width: 0, height: 0 } }
const renderedVideo = {
  hidden: false,
  visible: true,
  hasLiveVideo: true,
  readyState: 4,
  videoWidth: 1280,
  videoHeight: 720,
  frames: 12
}

test('screen-share validator accepts the sharer placeholder and requires remote rendering', () => {
  const local = {
    name: 'Tom',
    screenSharing: true,
    activeScreenShareParticipant: 'Tom',
    roomLayout: 'screen-share',
    screenStageLocalShare: true,
    screenStagePlaceholder: visibleRect,
    screenStageVideo: null,
    screenShareStripTiles: [
      { participant: 'Tom', rect: hiddenRect },
      { participant: 'Caitlyn', rect: visibleRect }
    ]
  }
  assert.deepEqual(validateScreenShareSnapshot(local, 'Tom', ['Tom', 'Caitlyn']), [])

  const remote = {
    ...local,
    name: 'Caitlyn',
    screenStageLocalShare: false,
    screenStagePlaceholder: hiddenRect,
    screenStageVideo: renderedVideo
  }
  assert.deepEqual(validateScreenShareSnapshot(remote, 'Tom', ['Tom', 'Caitlyn']), [])

  const failures = validateScreenShareSnapshot({ ...remote, screenStageVideo: null }, 'Tom', ['Tom', 'Caitlyn'])
  assert.match(failures.join('\n'), /remote stage video did not render/)
})

test('mobile pinned mode keeps canonical hero and strip while desktop stays single-stage', () => {
  const snapshot = {
    name: 'Tom',
    roomLayout: 'pinned',
    stageParticipant: 'Caitlyn',
    viewport: { innerWidth: 390, coarsePointer: true, maxTouchPoints: 5, hoverNone: true },
    tiles: [
      { participant: 'Tom', classes: 'video-tile', rect: visibleRect, renderedVideos: 1, decodedFrames: 12 },
      { participant: 'Caitlyn', classes: 'video-tile is-mobile-hero is-on-stage', rect: visibleRect, renderedVideos: 1, decodedFrames: 12 }
    ]
  }
  assert.deepEqual(validatePinnedViewSnapshot(snapshot, ['Tom', 'Caitlyn']), [])

  const desktopFailures = validatePinnedViewSnapshot({
    ...snapshot,
    viewport: { innerWidth: 1280, coarsePointer: false, maxTouchPoints: 0, hoverNone: false }
  }, ['Tom', 'Caitlyn'])
  assert.match(desktopFailures.join('\n'), /pinned view has 2 visible participant tiles/)
})

function soakSnapshot(iteration, options = {}) {
  const step = Number(iteration)
  const visible = options.remoteVisible !== false
  return {
    name: 'Tom',
    soakIteration: step,
    localTracks: [
      { kind: 'audio', readyState: 'live', enabled: true },
      { kind: 'video', readyState: 'live', enabled: true }
    ],
    localVideo: {
      frames: options.localFrames ?? step * 30,
      currentTime: options.localTime ?? step,
      attachmentRevision: options.localAttachmentRevision ?? 4
    },
    videoAttachmentRevision: options.videoAttachmentRevision ?? 10,
    mediaProgress: {
      outboundAudioBytes: options.outboundAudioBytes ?? step * 1000,
      outboundVideoBytes: options.outboundVideoBytes ?? step * 5000,
      outboundVideoFrames: options.outboundVideoFrames ?? step * 30
    },
    remoteVideoProgress: [{
      participant: 'Caitlyn',
      cameraOff: false,
      inboundBytesReceived: options.inboundBytesReceived ?? step * 6000,
      inboundFramesDecoded: options.inboundFramesDecoded ?? step * 30,
      visibleVideoCount: visible ? 1 : 0,
      renderedFrames: options.renderedFrames ?? (visible ? step * 30 : 0),
      renderCurrentTime: options.renderCurrentTime ?? (visible ? step : 0)
    }]
  }
}

test('soak validator requires continuous local, outbound, inbound, and visible render progress', () => {
  const snapshots = [soakSnapshot(1), soakSnapshot(2), soakSnapshot(3)]
  assert.deepEqual(validateSoakProgressSnapshots(snapshots, ['Tom', 'Caitlyn']), [])

  const stalled = [
    soakSnapshot(1),
    soakSnapshot(2, {
      outboundVideoBytes: 5000,
      outboundVideoFrames: 30,
      inboundBytesReceived: 6000,
      inboundFramesDecoded: 30,
      renderedFrames: 30,
      renderCurrentTime: 1,
      videoAttachmentRevision: 16
    })
  ]
  const failures = validateSoakProgressSnapshots(stalled, ['Tom', 'Caitlyn'], { attachmentRevisionBudget: 2 })
  const report = failures.join('\n')
  assert.match(report, /video attachment revision churned 6 times/)
  assert.match(report, /outbound video did not advance/)
  assert.match(report, /inbound video did not advance for Caitlyn/)
  assert.match(report, /visible remote video did not render new frames for Caitlyn/)
})

test('soak validator does not demand render progress from intentionally hidden board videos', () => {
  const snapshots = [
    soakSnapshot(1, { remoteVisible: false }),
    soakSnapshot(2, { remoteVisible: false })
  ]
  assert.deepEqual(validateSoakProgressSnapshots(snapshots, ['Tom', 'Caitlyn']), [])
})
