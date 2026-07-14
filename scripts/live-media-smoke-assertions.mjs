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

export function validateVideoTileMediaBounds(snapshot, tolerancePx = 2) {
  const failures = []
  const tolerance = Math.max(0, Number(tolerancePx) || 0)
  for (const tile of snapshot?.tiles || []) {
    if (!rectProbeVisible(tile.rect)) {
      continue
    }
    const tileRect = tile.rect?.rect || {}
    const tileWidth = Number(tileRect.width) || 0
    const tileHeight = Number(tileRect.height) || 0
    if (tileWidth <= 0 || tileHeight <= 0) {
      continue
    }
    for (const video of tile.videoDetails || []) {
      if (!videoProbeRendered(video) || !rectProbeVisible(video.rect)) {
        continue
      }
      const videoRect = video.rect?.rect || {}
      const videoWidth = Number(videoRect.width) || 0
      const videoHeight = Number(videoRect.height) || 0
      if (Math.abs(videoWidth - tileWidth) > tolerance || Math.abs(videoHeight - tileHeight) > tolerance) {
        failures.push(`${snapshot.name} video tile ${tile.participant || 'unknown'} has a ${videoWidth.toFixed(1)}x${videoHeight.toFixed(1)} media box inside a ${tileWidth.toFixed(1)}x${tileHeight.toFixed(1)} tile`)
      }
    }
  }
  return failures
}

export function usesMobileMediaLayout(snapshot) {
  const viewport = snapshot?.viewport || {}
  const visualWidth = Number(viewport.visualViewport?.width) || 0
  const visualHeight = Number(viewport.visualViewport?.height) || 0
  const innerWidth = Number(viewport.innerWidth) || 0
  const innerHeight = Number(viewport.innerHeight) || 0
  const documentWidth = Number(viewport.documentSize?.clientWidth) || 0
  const documentHeight = Number(viewport.documentSize?.clientHeight) || 0
  const width = visualWidth || innerWidth || documentWidth
  const height = visualHeight || innerHeight || documentHeight
  const phoneViewport = (width > 0 && width <= 700) || (height > 0 && height <= 500)
  return Boolean(phoneViewport && (viewport.coarsePointer || viewport.maxTouchPoints > 0 || viewport.hoverNone))
}

function classNames(value) {
  return String(value || '').split(/\s+/).filter(Boolean)
}

export function validateMobileRoomLayoutSnapshot(snapshot, expectedNames = []) {
  if (!usesMobileMediaLayout(snapshot)) {
    return []
  }
  const failures = []
  const prefix = `${snapshot.name} mobile room`
  const expected = expectedNames.filter(Boolean)
  const tiles = Array.isArray(snapshot.tiles) ? snapshot.tiles : []
  const canonicalTiles = expected.map(name => tiles.find(tile => tile.participant === name)).filter(Boolean)
  if (canonicalTiles.length !== expected.length) {
    failures.push(`${prefix} has ${canonicalTiles.length} canonical participant tiles, expected ${expected.length}`)
  }

  const laidOutTiles = canonicalTiles.filter(tile => rectProbeVisible(tile.rect))
  if (laidOutTiles.length !== expected.length) {
    failures.push(`${prefix} has ${laidOutTiles.length} laid-out participant tiles, expected ${expected.length}`)
  }
  const heroTiles = laidOutTiles.filter(tile => classNames(tile.classes).includes('is-mobile-hero'))
  if (heroTiles.length !== 1) {
    failures.push(`${prefix} has ${heroTiles.length} mobile heroes`)
    return failures
  }

  const hero = heroTiles[0]
  if (snapshot.roomLayout === 'pinned' && snapshot.stageParticipant && hero.participant !== snapshot.stageParticipant) {
    failures.push(`${prefix} pinned hero is ${hero.participant}, expected ${snapshot.stageParticipant}`)
  }
  if (snapshot.roomLayout === 'grid' && snapshot.serverActiveSpeakerFresh && snapshot.activeSpeaker && hero.participant !== snapshot.activeSpeaker) {
    failures.push(`${prefix} active-speaker hero is ${hero.participant}, expected ${snapshot.activeSpeaker}`)
  }

  const heroRect = hero.rect?.rect || {}
  const stripTiles = laidOutTiles.filter(tile => tile !== hero)
  const stripWidths = stripTiles.map(tile => Number(tile.rect?.rect?.width) || 0)
  const stripHeights = stripTiles.map(tile => Number(tile.rect?.rect?.height) || 0)
  if (stripWidths.some(width => width < 96)) {
    failures.push(`${prefix} strip tile width fell below 96px (${stripWidths.map(width => width.toFixed(1)).join(',')})`)
  }
  const maxStripHeight = Math.max(0, ...stripHeights)
  if (maxStripHeight > 0 && (Number(heroRect.height) || 0) < maxStripHeight * 2) {
    failures.push(`${prefix} hero height ${(Number(heroRect.height) || 0).toFixed(1)}px does not dominate ${maxStripHeight.toFixed(1)}px strip`)
  }

  const viewportWidth = Number(snapshot.viewport?.visualViewport?.width)
    || Number(snapshot.viewport?.innerWidth)
    || Number(snapshot.viewport?.documentSize?.clientWidth)
    || 0
  const viewportHeight = Number(snapshot.viewport?.visualViewport?.height)
    || Number(snapshot.viewport?.innerHeight)
    || Number(snapshot.viewport?.documentSize?.clientHeight)
    || 0
  const landscape = viewportWidth > viewportHeight
  const minimumHeroWidth = landscape ? viewportWidth * 0.6 : viewportWidth - 48
  if (viewportWidth > 0 && (Number(heroRect.width) || 0) < minimumHeroWidth) {
    failures.push(`${prefix} hero width ${(Number(heroRect.width) || 0).toFixed(1)}px is too narrow for ${viewportWidth}px viewport`)
  }

  const stack = snapshot.videoStackGeometry || {}
  const filmstripScrollable = landscape
    ? Number(stack.scrollHeight) > Number(stack.clientHeight)
    : Number(stack.scrollWidth) > Number(stack.clientWidth)
  if (expected.length >= 5 && !filmstripScrollable) {
    failures.push(`${prefix} crowded filmstrip is not scroll reachable`)
  }
  return failures
}

export function validateMobileRoomChromeSnapshot(snapshot) {
  if (!usesMobileMediaLayout(snapshot)) {
    return []
  }
  const failures = []
  const prefix = `${snapshot.name} mobile room chrome`
  if (snapshot.phoneLayoutMatches !== true) {
    failures.push(`${prefix} CSS and JS disagree about the phone layout`)
  }
  if (!snapshot.pipMeeting?.hidden || snapshot.pipMeeting?.visible) {
    failures.push(`${prefix} desktop PiP is active on the phone layout`)
  }
  if (snapshot.toolRail?.visible) {
    failures.push(`${prefix} still shows the global tool rail over the live room`)
  }
  if (!snapshot.meetingBar?.visible || !rectProbeVisible(snapshot.meetingBar.rect)) {
    failures.push(`${prefix} call dock is not visible`)
  }
  if (!snapshot.topbarBack?.visible || !rectProbeVisible(snapshot.topbarBack.rect)) {
    failures.push(`${prefix} minimize control is not visible`)
  } else {
    const width = Number(snapshot.topbarBack.rect?.rect?.width) || 0
    const height = Number(snapshot.topbarBack.rect?.rect?.height) || 0
    if (width < 44 || height < 44) {
      failures.push(`${prefix} minimize hit area is ${width.toFixed(1)}x${height.toFixed(1)}`)
    }
  }
  const visibleControlIds = (snapshot.callControls || []).filter(control => control.visible).map(control => control.id)
  for (const id of ['muteMic', 'toggleCamera', 'roomChatToggle', 'roomMoreToggle', 'leave']) {
    if (!visibleControlIds.includes(id)) {
      failures.push(`${prefix} is missing primary call action ${id}`)
    }
  }
  for (const id of ['recordMeeting', 'inviteToggle', 'archiveMeeting']) {
    if (visibleControlIds.includes(id)) {
      failures.push(`${prefix} still exposes secondary action ${id} in the primary dock`)
    }
  }
  for (const control of snapshot.callControls || []) {
    if (!control.visible) {
      continue
    }
    const width = Number(control.rect?.rect?.width) || 0
    const height = Number(control.rect?.rect?.height) || 0
    if (width < 44 || height < 44) {
      failures.push(`${prefix} ${control.id} hit area is ${width.toFixed(1)}x${height.toFixed(1)}`)
    }
    if (!String(control.ariaLabel || '').trim()) {
      failures.push(`${prefix} ${control.id} has no accessible label`)
    }
  }
  const barRect = snapshot.meetingBar?.rect?.rect || {}
  const stackRect = snapshot.videoStackGeometry?.rect?.rect || {}
  if ((Number(stackRect.bottom) || 0) > (Number(barRect.top) || 0)) {
    failures.push(`${prefix} call dock overlaps the participant stage`)
  }
  const hero = (snapshot.tiles || []).find(tile => classNames(tile.classes).includes('is-mobile-hero'))
  const heroRect = hero?.rect?.rect || {}
  if ((Number(heroRect.bottom) || 0) > (Number(barRect.top) || 0)) {
    failures.push(`${prefix} call dock overlaps the active-speaker hero`)
  }
  return failures
}

export function validateMobileActiveSpeakerSnapshots(snapshots) {
  const failures = []
  const attachmentBaseline = new Map()
  for (const snapshot of snapshots) {
    if (!usesMobileMediaLayout(snapshot)) {
      continue
    }
    const expected = String(snapshot.expectedActiveSpeaker || '')
    const prefix = `${snapshot.name} mobile speaker ${expected || 'missing'}`
    const heroTiles = (snapshot.tiles || []).filter(tile => classNames(tile.classes).includes('is-mobile-hero'))
    if (!expected || snapshot.activeSpeaker !== expected || !snapshot.serverActiveSpeakerFresh) {
      failures.push(`${prefix} did not retain a fresh authoritative active speaker`)
    }
    if (heroTiles.length !== 1 || heroTiles[0]?.participant !== expected) {
      failures.push(`${prefix} hero is ${heroTiles.map(tile => tile.participant).join(',') || 'missing'}`)
    }
    const speakerTile = (snapshot.tiles || []).find(tile => tile.participant === expected)
    if (!speakerTile || !classNames(speakerTile.classes).includes('is-active-speaker')) {
      failures.push(`${prefix} has no audible active-speaker ring`)
    }
    const revision = Number(snapshot.videoAttachmentRevision) || 0
    const baseline = attachmentBaseline.get(snapshot.name)
    if (baseline == null) {
      attachmentBaseline.set(snapshot.name, revision)
    } else if (revision !== baseline) {
      failures.push(`${prefix} changed video attachment revision ${baseline}->${revision}`)
    }
  }
  return failures
}

export function validateMobilePinInteractions(interactions, participantCount = 0) {
  const failures = []
  for (const interaction of interactions || []) {
    if (!interaction?.mobile) {
      continue
    }
    const prefix = `${interaction.name} mobile filmstrip target ${interaction.target || 'missing'}`
    if (!interaction.clicked) {
      failures.push(`${prefix} was not promoted through its actual pin control`)
    }
    if (!interaction.hitTargetedButton) {
      failures.push(`${prefix} pin control does not own center-point hit testing`)
    }
    if (participantCount >= 5 && !interaction.wasOffscreen) {
      failures.push(`${prefix} did not exercise an offscreen tile`)
    }
    if (participantCount >= 5 && !interaction.scrolled) {
      failures.push(`${prefix} did not move the filmstrip scroll position`)
    }
  }
  return failures
}

export function validateMobileMoreMenuSnapshots(snapshots) {
  const failures = []
  for (const snapshot of snapshots || []) {
    const prefix = `${snapshot.name} mobile more menu`
    if (!snapshot.menuVisible) {
      failures.push(`${prefix} did not open`)
      continue
    }
    const visibleActions = (snapshot.actions || []).filter(action => !action.hidden)
    for (const id of ['roomMoreRecord', 'roomMoreInvite', 'roomMoreArchive']) {
      const action = visibleActions.find(candidate => candidate.id === id)
      if (!action) {
        failures.push(`${prefix} is missing ${id}`)
        continue
      }
      if (!String(action.label || '').trim()) {
        failures.push(`${prefix} ${id} has no label`)
      }
      if ((Number(action.rect?.width) || 0) < 44 || (Number(action.rect?.height) || 0) < 44) {
        failures.push(`${prefix} ${id} hit area is too small`)
      }
    }
  }
  return failures
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
  failures.push(...validateMobileRoomLayoutSnapshot(snapshot, expectedNames))
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
