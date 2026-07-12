export function rectProbeVisible(probe) {
  const rect = probe?.rect || {}
  return (Number(rect.width) || 0) > 0 && (Number(rect.height) || 0) > 0
}

export function videoProbeRendered(probe) {
  return Boolean(probe
    && !probe.hidden
    && probe.visible
    && probe.hasLiveVideo
    && ((probe.readyState >= 2 && probe.videoWidth > 0 && probe.videoHeight > 0) || probe.frames > 0))
}

export function usesMobileMediaLayout(snapshot) {
  const viewport = snapshot?.viewport || {}
  const visualWidth = Number(viewport.visualViewport?.width) || 0
  const innerWidth = Number(viewport.innerWidth) || 0
  const documentWidth = Number(viewport.documentSize?.clientWidth) || 0
  const width = visualWidth || innerWidth || documentWidth
  return Boolean(width > 0 && width <= 700 && (viewport.coarsePointer || viewport.maxTouchPoints > 0 || viewport.hoverNone))
}

export function validatePinnedViewSnapshot(snapshot, expectedNames = []) {
  const failures = []
  const prefix = `${snapshot.name} pin`
  if (snapshot.roomLayout !== 'pinned') {
    failures.push(`${prefix} room layout is ${snapshot.roomLayout}`)
  }
  if (!snapshot.stageParticipant) {
    failures.push(`${prefix} has no pinned participant`)
  }
  const pinnedTile = snapshot.tiles.find(tile => tile.participant === snapshot.stageParticipant)
  if (!pinnedTile) {
    failures.push(`${prefix} is missing pinned tile for ${snapshot.stageParticipant}`)
  } else if (pinnedTile.renderedVideos <= 0 && pinnedTile.decodedFrames <= 0) {
    failures.push(`${prefix} pinned tile did not render for ${snapshot.stageParticipant}`)
  }

  const visibleTiles = snapshot.tiles.filter(tile => rectProbeVisible(tile.rect))
  if (!usesMobileMediaLayout(snapshot)) {
    if (visibleTiles.length !== 1 || visibleTiles[0]?.participant !== snapshot.stageParticipant) {
      failures.push(`${prefix} pinned view has ${visibleTiles.length} visible participant tiles`)
    }
    return failures
  }

  // Mobile pinned mode deliberately keeps the canonical hero + strip tiles in
  // the DOM. The pinned participant is the one is-mobile-hero tile; hiding the
  // strip would regress the current two-person mobile design.
  const expectedVisibleNames = expectedNames.filter(Boolean)
  if (visibleTiles.length !== expectedVisibleNames.length) {
    failures.push(`${prefix} mobile pinned view has ${visibleTiles.length} visible participant tiles, expected ${expectedVisibleNames.length}`)
  }
  for (const name of expectedVisibleNames) {
    if (!visibleTiles.some(tile => tile.participant === name)) {
      failures.push(`${prefix} mobile pinned view is missing visible canonical tile for ${name}`)
    }
  }
  const heroTiles = visibleTiles.filter(tile => String(tile.classes || '').split(/\s+/).includes('is-mobile-hero'))
  if (heroTiles.length !== 1 || heroTiles[0]?.participant !== snapshot.stageParticipant) {
    failures.push(`${prefix} mobile pinned hero is ${heroTiles.map(tile => tile.participant).join(',') || 'missing'}, expected ${snapshot.stageParticipant}`)
  }
  return failures
}

export function validateScreenShareSnapshot(snapshot, sharerName, expectedNames = []) {
  const failures = []
  const prefix = `${snapshot.name} screen share`
  if (!snapshot.screenSharing || snapshot.activeScreenShareParticipant !== sharerName) {
    failures.push(`${prefix} is not showing ${sharerName}'s share`)
  }
  if (snapshot.roomLayout !== 'screen-share') {
    failures.push(`${prefix} room layout is ${snapshot.roomLayout}`)
  }

  if (snapshot.name === sharerName) {
    if (!snapshot.screenStageLocalShare) {
      failures.push(`${prefix} is missing the intentional local-share placeholder state`)
    }
    if (!rectProbeVisible(snapshot.screenStagePlaceholder)) {
      failures.push(`${prefix} local-share placeholder is not visible`)
    }
    if (videoProbeRendered(snapshot.screenStageVideo)) {
      failures.push(`${prefix} unexpectedly mirrors the local shared screen`)
    }
  } else {
    if (snapshot.screenStageLocalShare) {
      failures.push(`${prefix} incorrectly uses the local-share placeholder`)
    }
    if (!videoProbeRendered(snapshot.screenStageVideo)) {
      failures.push(`${prefix} remote stage video did not render`)
    }
  }

  const visibleStripTiles = snapshot.screenShareStripTiles.filter(tile => rectProbeVisible(tile.rect))
  const expectedStripNames = expectedNames.filter(name => name && name !== sharerName)
  if (visibleStripTiles.length !== expectedStripNames.length) {
    failures.push(`${prefix} participant strip has ${visibleStripTiles.length} visible tiles, expected ${expectedStripNames.length}`)
  }
  for (const name of expectedStripNames) {
    if (!visibleStripTiles.some(tile => tile.participant === name)) {
      failures.push(`${prefix} participant strip is missing ${name}`)
    }
  }
  return failures
}

function mediaCounter(snapshot, key) {
  return Number(snapshot?.mediaProgress?.[key]) || 0
}

function videoAdvanced(previous, current) {
  return (Number(current?.frames) || 0) > (Number(previous?.frames) || 0)
    || (Number(current?.currentTime) || 0) > (Number(previous?.currentTime) || 0) + 0.01
}

function remoteProgressByName(snapshot) {
  return new Map((snapshot?.remoteVideoProgress || []).map(progress => [String(progress.participant || '').toLowerCase(), progress]))
}

export function validateSoakProgressSnapshots(snapshots, expectedNames = [], options = {}) {
  if (!snapshots.length) {
    return []
  }
  const failures = []
  const byObserver = new Map()
  for (const snapshot of snapshots) {
    const observer = String(snapshot.name || '')
    if (!byObserver.has(observer)) {
      byObserver.set(observer, [])
    }
    byObserver.get(observer).push(snapshot)
  }

  for (const [observer, observerSnapshots] of byObserver) {
    observerSnapshots.sort((left, right) => (Number(left.soakIteration) || 0) - (Number(right.soakIteration) || 0))
    if (observerSnapshots.length < 2) {
      failures.push(`${observer} soak has ${observerSnapshots.length} snapshot; need at least 2 to prove progress`)
      continue
    }

    const attachmentBudget = Number.isFinite(options.attachmentRevisionBudget)
      ? Number(options.attachmentRevisionBudget)
      : Math.max(2, expectedNames.length)
    const first = observerSnapshots[0]
    const last = observerSnapshots[observerSnapshots.length - 1]
    const attachmentChurn = (Number(last.videoAttachmentRevision) || 0) - (Number(first.videoAttachmentRevision) || 0)
    if (attachmentChurn < 0) {
      failures.push(`${observer} soak video attachment revision regressed ${first.videoAttachmentRevision}->${last.videoAttachmentRevision}`)
    } else if (attachmentChurn > attachmentBudget) {
      failures.push(`${observer} soak video attachment revision churned ${attachmentChurn} times, budget ${attachmentBudget}`)
    }
    const localAttachmentChurn = (Number(last.localVideo?.attachmentRevision) || 0) - (Number(first.localVideo?.attachmentRevision) || 0)
    if (localAttachmentChurn < 0 || localAttachmentChurn > 1) {
      failures.push(`${observer} soak local video attachment revision churned ${localAttachmentChurn} times, budget 1`)
    }

    for (let index = 1; index < observerSnapshots.length; index++) {
      const previous = observerSnapshots[index - 1]
      const current = observerSnapshots[index]
      const prefix = `${observer} soak ${previous.soakIteration}->${current.soakIteration}`
      const localTrack = current.localTracks?.find(track => track.kind === 'video')
      if (localTrack?.readyState === 'live' && localTrack.enabled && !videoAdvanced(previous.localVideo, current.localVideo)) {
        failures.push(`${prefix} local video did not advance`)
      }

      const outboundVideoAdvanced = mediaCounter(current, 'outboundVideoFrames') > mediaCounter(previous, 'outboundVideoFrames')
        || mediaCounter(current, 'outboundVideoBytes') > mediaCounter(previous, 'outboundVideoBytes')
      if (localTrack?.readyState === 'live' && localTrack.enabled && !outboundVideoAdvanced) {
        failures.push(`${prefix} outbound video did not advance`)
      }
      const localAudioTrack = current.localTracks?.find(track => track.kind === 'audio')
      if (localAudioTrack?.readyState === 'live' && localAudioTrack.enabled
          && mediaCounter(current, 'outboundAudioBytes') <= mediaCounter(previous, 'outboundAudioBytes')) {
        failures.push(`${prefix} outbound audio did not advance`)
      }

      const previousRemote = remoteProgressByName(previous)
      const currentRemote = remoteProgressByName(current)
      for (const participant of expectedNames.filter(name => name && name !== observer)) {
        const key = participant.toLowerCase()
        const before = previousRemote.get(key)
        const after = currentRemote.get(key)
        if (!before || !after) {
          failures.push(`${prefix} has no progress probe for remote ${participant}`)
          continue
        }
        if (after.cameraOff) {
          continue
        }
        const inboundAdvanced = (Number(after.inboundFramesDecoded) || 0) > (Number(before.inboundFramesDecoded) || 0)
          || (Number(after.inboundBytesReceived) || 0) > (Number(before.inboundBytesReceived) || 0)
        if (!inboundAdvanced) {
          failures.push(`${prefix} inbound video did not advance for ${participant}`)
        }

        // Rendering is required only while a participant video is visibly on
        // this surface. Board-expanded mobile intentionally hides its dock, so
        // transport progress remains authoritative there without a false fail.
        if ((Number(before.visibleVideoCount) || 0) > 0 && (Number(after.visibleVideoCount) || 0) > 0) {
          const renderAdvanced = (Number(after.renderedFrames) || 0) > (Number(before.renderedFrames) || 0)
            || (Number(after.renderCurrentTime) || 0) > (Number(before.renderCurrentTime) || 0) + 0.01
          if (!renderAdvanced) {
            failures.push(`${prefix} visible remote video did not render new frames for ${participant}`)
          }
        }
      }
    }
  }
  return failures
}
