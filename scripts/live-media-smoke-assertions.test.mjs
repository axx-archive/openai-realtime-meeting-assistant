import assert from 'node:assert/strict'
import test from 'node:test'

import {
  mediaSoakProbeURL,
  validateMobileActiveSpeakerSnapshots,
  validateMobileMoreMenuSnapshots,
  validateMobilePinInteractions,
  validateMobileRoomChromeSnapshot,
  validateMobileRoomLayoutSnapshot,
  validatePinnedViewSnapshot,
  screenShareFrameHasSignal,
  validateScreenShareSnapshot,
  validateSoakProgressSnapshots,
  validateVideoTileMediaBounds
} from './live-media-smoke-assertions.mjs'

test('media-soak probe query is present only in explicit collector mode', () => {
  assert.equal(mediaSoakProbeURL('https://example.test/rooms/alpha?x=1', true), 'https://example.test/rooms/alpha?x=1&media-soak-probe=1')
  assert.equal(mediaSoakProbeURL('https://example.test/rooms/alpha?x=1&media-soak-probe=1', false), 'https://example.test/rooms/alpha?x=1')
})

const visibleRect = { clientRects: 1, rect: { top: 0, right: 320, bottom: 180, left: 0, width: 320, height: 180 } }
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
const colorfulScreenSharePixels = {
  sampled: true,
  sampleCount: 2304,
  nonBlackPixels: 2100,
  nonBlackRatio: 2100 / 2304,
  meanLuma: 0.34,
  maxLuma: 1
}
const blackScreenSharePixels = {
  sampled: true,
  sampleCount: 2304,
  nonBlackPixels: 0,
  nonBlackRatio: 0,
  meanLuma: 0,
  maxLuma: 0
}

test('gallery media boxes stay pinned to their tile instead of expanding to portrait intrinsic height', () => {
  const snapshot = {
    name: 'AJ',
    tiles: [{
      participant: 'Erick',
      rect: { clientRects: 1, rect: { width: 965, height: 603.125 } },
      videoDetails: [{
        ...renderedVideo,
        videoWidth: 720,
        videoHeight: 1280,
        rect: { clientRects: 1, rect: { width: 965, height: 603.125 } }
      }]
    }]
  }
  assert.deepEqual(validateVideoTileMediaBounds(snapshot), [])

  const failures = validateVideoTileMediaBounds({
    ...snapshot,
    tiles: snapshot.tiles.map(tile => ({
      ...tile,
      videoDetails: tile.videoDetails.map(video => ({
        ...video,
        rect: { clientRects: 1, rect: { width: 965, height: 1715.55 } }
      }))
    }))
  })
  assert.match(failures.join('\n'), /965\.0x1715\.5 media box inside a 965\.0x603\.1 tile/)
})

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
    screenStageVideo: renderedVideo,
    screenStageFramePixels: colorfulScreenSharePixels
  }
  assert.deepEqual(validateScreenShareSnapshot(remote, 'Tom', ['Tom', 'Caitlyn']), [])
  assert.equal(screenShareFrameHasSignal(colorfulScreenSharePixels), true)

  const failures = validateScreenShareSnapshot({ ...remote, screenStageVideo: null }, 'Tom', ['Tom', 'Caitlyn'])
  assert.match(failures.join('\n'), /remote stage video did not render/)

  const blackFailures = validateScreenShareSnapshot({ ...remote, screenStageFramePixels: blackScreenSharePixels }, 'Tom', ['Tom', 'Caitlyn'])
  assert.equal(screenShareFrameHasSignal(blackScreenSharePixels), false)
  assert.match(blackFailures.join('\n'), /remote stage decoded only black pixels/)
})

test('mobile pinned mode keeps canonical hero and strip while desktop stays single-stage', () => {
  const heroRect = { clientRects: 1, rect: { top: 0, right: 366, bottom: 484, left: 0, width: 366, height: 484 } }
  const stripRect = { clientRects: 1, rect: { top: 492, right: 104, bottom: 596, left: 0, width: 104, height: 104 } }
  const snapshot = {
    name: 'Tom',
    roomLayout: 'pinned',
    stageParticipant: 'Caitlyn',
    viewport: { innerWidth: 390, innerHeight: 844, coarsePointer: true, maxTouchPoints: 5, hoverNone: true },
    tiles: [
      { participant: 'Tom', classes: 'video-tile', rect: stripRect, renderedVideos: 1, decodedFrames: 12 },
      { participant: 'Caitlyn', classes: 'video-tile is-mobile-hero is-on-stage', rect: heroRect, renderedVideos: 1, decodedFrames: 12 }
    ],
    videoStackGeometry: { clientWidth: 366, scrollWidth: 366 }
  }
  assert.deepEqual(validatePinnedViewSnapshot(snapshot, ['Tom', 'Caitlyn']), [])

  const desktopFailures = validatePinnedViewSnapshot({
    ...snapshot,
    viewport: { innerWidth: 1280, coarsePointer: false, maxTouchPoints: 0, hoverNone: false }
  }, ['Tom', 'Caitlyn'])
  assert.match(desktopFailures.join('\n'), /pinned view has 2 visible participant tiles/)
})

test('mobile crowded-room validator requires a dominant active-speaker hero and reachable filmstrip', () => {
  const names = ['Tom', 'Caitlyn', 'Tyler', 'Tim', 'Erick']
  const snapshot = {
    name: 'Tom',
    roomLayout: 'grid',
    activeSpeaker: 'Caitlyn',
    serverActiveSpeakerFresh: true,
    viewport: { innerWidth: 390, innerHeight: 844, coarsePointer: true, maxTouchPoints: 5, hoverNone: true },
    videoStackGeometry: { clientWidth: 366, scrollWidth: 440 },
    tiles: names.map((name, index) => ({
      participant: name,
      classes: name === 'Caitlyn' ? 'video-tile is-mobile-hero is-active-speaker' : 'video-tile',
      rect: name === 'Caitlyn'
        ? { clientRects: 1, rect: { top: 0, right: 366, bottom: 484, left: 0, width: 366, height: 484 } }
        : { clientRects: 1, rect: { top: 492, right: 104 + index * 112, bottom: 596, left: index * 112, width: 104, height: 104 } }
    }))
  }
  assert.deepEqual(validateMobileRoomLayoutSnapshot(snapshot, names), [])

  const failures = validateMobileRoomLayoutSnapshot({
    ...snapshot,
    videoStackGeometry: { clientWidth: 366, scrollWidth: 366 },
    tiles: snapshot.tiles.map(tile => tile.participant === 'Caitlyn'
      ? { ...tile, rect: { clientRects: 1, rect: { width: 85, height: 104 } } }
      : tile)
  }, names)
  assert.match(failures.join('\n'), /hero width|does not dominate|not scroll reachable/)
})

test('mobile landscape keeps a dominant hero beside a vertically reachable filmstrip', () => {
  const names = ['Tom', 'Caitlyn', 'Tyler', 'Tim', 'Erick']
  const snapshot = {
    name: 'Tom',
    roomLayout: 'grid',
    activeSpeaker: 'Tyler',
    serverActiveSpeakerFresh: true,
    viewport: { innerWidth: 844, innerHeight: 390, coarsePointer: true, maxTouchPoints: 5, hoverNone: true },
    videoStackGeometry: { clientHeight: 286, scrollHeight: 344 },
    tiles: names.map((name, index) => ({
      participant: name,
      classes: name === 'Tyler' ? 'video-tile is-mobile-hero is-active-speaker' : 'video-tile',
      rect: name === 'Tyler'
        ? { clientRects: 1, rect: { top: 0, right: 704, bottom: 286, left: 0, width: 704, height: 286 } }
        : { clientRects: 1, rect: { top: index * 88, right: 820, bottom: index * 88 + 80, left: 716, width: 104, height: 80 } }
    }))
  }
  assert.deepEqual(validateMobileRoomLayoutSnapshot(snapshot, names), [])
})

test('mobile room chrome keeps one call dock, a minimize control, and 44px actions', () => {
  const control = id => ({
    id,
    visible: true,
    ariaLabel: id,
    rect: { clientRects: 1, rect: { width: 44, height: 44 } }
  })
  const snapshot = {
    name: 'Tom',
    viewport: { innerWidth: 390, innerHeight: 844, coarsePointer: true, maxTouchPoints: 5, hoverNone: true },
    phoneLayoutMatches: true,
    pipMeeting: { hidden: true, visible: false },
    toolRail: { visible: false },
    meetingBar: { visible: true, rect: { clientRects: 1, rect: { top: 768, bottom: 828, width: 371, height: 60 } } },
    topbarBack: { visible: true, rect: { clientRects: 1, rect: { width: 44, height: 44 } } },
    videoStackGeometry: { rect: { clientRects: 1, rect: { top: 100, bottom: 760, width: 366, height: 660 } } },
    tiles: [{ participant: 'Caitlyn', classes: 'video-tile is-mobile-hero', rect: { clientRects: 1, rect: { top: 100, bottom: 760, width: 366, height: 660 } } }],
    callControls: [
      ...['muteMic', 'toggleCamera', 'roomChatToggle', 'roomMoreToggle', 'leave'].map(control),
      ...['recordMeeting', 'inviteToggle', 'archiveMeeting'].map(id => ({ ...control(id), visible: false }))
    ]
  }
  assert.deepEqual(validateMobileRoomChromeSnapshot(snapshot), [])

  const failures = validateMobileRoomChromeSnapshot({
    ...snapshot,
    phoneLayoutMatches: false,
    pipMeeting: { hidden: false, visible: true },
    toolRail: { visible: true },
    topbarBack: { visible: true, rect: { clientRects: 1, rect: { width: 28, height: 28 } } },
    tiles: [{ participant: 'Caitlyn', classes: 'video-tile is-mobile-hero', rect: { clientRects: 1, rect: { top: 100, bottom: 780, width: 366, height: 680 } } }],
    callControls: [{ ...control('muteMic'), rect: { clientRects: 1, rect: { width: 40, height: 40 } } }]
  })
  assert.match(failures.join('\n'), /CSS and JS disagree|desktop PiP|global tool rail|minimize hit area|40\.0x40\.0|overlaps the active-speaker hero/)
})

test('mobile authoritative speaker promotion moves the canonical hero without attachment churn', () => {
  const snapshot = (expectedActiveSpeaker, revision = 7) => ({
    name: 'Tom',
    expectedActiveSpeaker,
    activeSpeaker: expectedActiveSpeaker,
    serverActiveSpeakerFresh: true,
    videoAttachmentRevision: revision,
    viewport: { innerWidth: 390, innerHeight: 844, coarsePointer: true, maxTouchPoints: 5, hoverNone: true },
    tiles: ['Tom', 'Caitlyn', 'Tyler'].map(name => ({
      participant: name,
      classes: name === expectedActiveSpeaker ? 'video-tile is-mobile-hero is-active-speaker' : 'video-tile'
    }))
  })
  assert.deepEqual(validateMobileActiveSpeakerSnapshots([
    snapshot('Caitlyn'),
    snapshot('Tyler')
  ]), [])

  const failures = validateMobileActiveSpeakerSnapshots([
    snapshot('Caitlyn'),
    snapshot('Tyler', 8)
  ])
  assert.match(failures.join('\n'), /attachment revision 7->8/)
})

test('mobile filmstrip gate requires a real scroll and pin-control click for crowded rooms', () => {
  assert.deepEqual(validateMobilePinInteractions([
    { name: 'Tom', target: 'Erick', mobile: true, wasOffscreen: true, scrolled: true, clicked: true, hitTargetedButton: true }
  ], 5), [])
  const failures = validateMobilePinInteractions([
    { name: 'Tom', target: 'Erick', mobile: true, wasOffscreen: false, scrolled: false, clicked: false, hitTargetedButton: false }
  ], 5)
  assert.match(failures.join('\n'), /actual pin control|center-point hit testing|offscreen tile|scroll position/)
})

test('mobile secondary call actions stay reachable in a labelled 44px menu', () => {
  const actions = ['roomMoreRecord', 'roomMoreInvite', 'roomMoreArchive'].map(id => ({
    id,
    hidden: false,
    label: id,
    rect: { width: 180, height: 44 }
  }))
  assert.deepEqual(validateMobileMoreMenuSnapshots([{ name: 'Tom', menuVisible: true, actions }]), [])
  assert.match(validateMobileMoreMenuSnapshots([{
    name: 'Tom',
    menuVisible: true,
    actions: actions.map(action => action.id === 'roomMoreInvite' ? { ...action, rect: { width: 180, height: 40 } } : action)
  }]).join('\n'), /roomMoreInvite hit area is too small/)
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
