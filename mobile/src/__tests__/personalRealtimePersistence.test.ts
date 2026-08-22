import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('private Scout realtime is owned above navigation and Canvas only controls it', () => {
  const root = source('src', 'navigation', 'RootNavigator.tsx');
  const provider = source('src', 'realtime', 'PersonalRealtimeProvider.tsx');
  const canvas = source('src', 'screens', 'CanvasScreen.tsx');
  const providerIndex = root.indexOf('<PersonalRealtimeProvider');
  const navigationIndex = root.indexOf('<NavigationContainer');
  const providerCloseIndex = root.indexOf('</PersonalRealtimeProvider>');

  assert.match(provider, /const realtime = usePersonalRealtime\(\{ onActions \}\)/);
  assert.ok(providerIndex >= 0 && providerIndex < navigationIndex);
  assert.ok(providerCloseIndex > navigationIndex);
  assert.match(canvas, /const realtime = usePersonalRealtimeContext\(\)/);
  assert.doesNotMatch(canvas, /from '\.\.\/realtime\/usePersonalRealtime'/);
  assert.match(canvas, /await runPersonalRealtimeTap\(realtime\)/);
  const sendOpening = canvas.slice(canvas.indexOf('const sendOpening'), canvas.indexOf('const voiceNotice'));
  assert.doesNotMatch(sendOpening, /realtime\.stop/);
});

test('global voice island is discoverable at rest and opens only the exact bound thread', () => {
  const root = source('src', 'navigation', 'RootNavigator.tsx');
  const shell = source('src', 'navigation', 'NativeUniversalShell.tsx');
  const control = source('src', 'realtime', 'PersonalRealtimeFloatingControl.tsx');
  const placement = source('src', 'realtime', 'personalRealtimeIslandPlacement.ts');
  const realtime = source('src', 'realtime', 'usePersonalRealtime.ts');
  const client = source('src', 'api', 'client.ts');

  assert.match(realtime, /setThreadId\(answer\.threadId\)/);
  assert.match(realtime, /voiceTransportRevisionRef\.current = answer\.transportRevision/);
  assert.match(realtime, /milestoneOperationIdsRef\.current\.get\(milestone\)/);
  assert.match(realtime, /pendingMilestonesRef\.current\.add\(milestone\)/);
  assert.match(realtime, /pendingMilestones\.forEach\(\(milestone\) => publishMilestone/);
  assert.match(realtime, /voiceSessionId,[\s\S]*threadId,[\s\S]*transportRevision,[\s\S]*operationId,[\s\S]*milestone/);
  assert.match(client, /transportRevision: number/);
  assert.match(client, /body: typeof milestoneOrBinding === "string"[\s\S]*: milestoneOrBinding/);
  assert.match(realtime, /threadId,[\s\S]*start,[\s\S]*stop/);
  assert.match(root, /personalRealtimeVisible=\{Boolean\([\s\S]*user[\s\S]*sessionToken[\s\S]*activeRoute !== 'Room'[\s\S]*nativeShellVisibleForRoute\(activeRoute\)[\s\S]*\)\}/);
  assert.match(root, /personalRealtimeStartAllowed=\{Boolean\(user && sessionToken && activeRoute !== 'Room'\)\}/);
  assert.match(root, /roomActive=\{activeRoute === 'Room'\}/);
  assert.match(shell, /realtime\.enabled && personalRealtimeVisible/);
  assert.match(shell, /realtime\.active/);
  assert.match(shell, /realtime\.tearingDown/);
  assert.match(shell, /realtime\.status === 'error'/);
  assert.match(shell, /startAllowed=\{personalRealtimeStartAllowed\}/);
  assert.match(shell, /personalRealtimeIslandPlacement\(/);
  assert.match(root, /personalRealtimeSurface=\{personalRealtimeIslandSurface\(activeRoute\)\}/);
  assert.match(shell, /personalRealtimePlacement\.docked && styles\.personalRealtimeDocked/);
  assert.doesNotMatch(shell.slice(shell.indexOf('personalRealtimePlacement')), /top: Math\.max\(insets\.top/);
  assert.match(placement, /FOCUSED_ACTIVE_LANE_HEIGHT = 64/);
  assert.match(placement, /FOCUSED_ERROR_LANE_HEIGHT = 168/);
  assert.match(placement, /route === 'Thread' \|\| route === 'ChannelRiff'/);
  assert.match(shell, /paddingTop: personalRealtimePlacement\.contentTopInset/);
  assert.match(source('src', 'realtime', 'PersonalRealtimeProvider.tsx'), /if \(roomActive && realtime\.active\) void realtime\.stop\('cancelled'\)/);
  assert.match(root, /navigationRef\.navigate\('Thread', \{ threadId, title: 'Scout voice' \}\)/);
  assert.match(shell, /onOpenThread=\{onOpenPersonalRealtimeThread\}/);
  assert.match(control, /personalRealtimeIslandModel\(/);
  assert.match(control, /island\.action === 'start' \|\| island\.action === 'retry'/);
  assert.match(control, /island\.action === 'open_thread' && realtime\.threadId/);
  assert.match(control, /onOpenThread\?\.\(realtime\.threadId\)/);
  assert.match(control, /island\.showClose/);
  assert.match(control, /accessibilityRole="alert"/);
  assert.match(control, /safePersonalRealtimeErrorMessage\(realtime\.error\)/);
  assert.match(control, /accessibilityLabel="Stop Scout voice"/);
  assert.match(control, /<Waveform/);
  assert.match(control, /realtime\.status === 'idle'[\s\S]*colors\.text2/);
  assert.match(control, /PERSONAL_REALTIME_ISLAND_WAVEFORM_WIDTH/);
  assert.match(control, /void realtime\.stop\(realtime\.status === 'error' \? 'cancelled' : 'completed'\)/);
  assert.match(control, /width: hitMin[\s\S]*height: hitMin/);
  assert.match(control, /transform: \[\{ scale: 0\.96 \}\]/);
});

test('persistence does not weaken server authority or room audio focus', () => {
  const realtime = source('src', 'realtime', 'usePersonalRealtime.ts');
  const room = source('src', 'realtime', 'useNativeRoom.ts');
  const root = source('src', 'navigation', 'RootNavigator.tsx');

  assert.match(realtime, /audioFocusRuntime\.acquire\('personal_realtime'/);
  assert.match(room, /audioFocusRuntime\.acquire\('meeting_media'/);
  assert.match(realtime, /api\.realtimeOffer\([\s\S]*sessionToken,[\s\S]*localSDP,[\s\S]*voiceSessionId,[\s\S]*createConversationOperationId\(\),[\s\S]*\)/);
  assert.match(realtime, /voiceThreadIdRef\.current/);
  assert.match(realtime, /voiceAuthorityTokenRef\.current = sessionToken/);
  assert.match(realtime, /const serverAuthorityToken = voiceAuthorityTokenRef\.current/);
  assert.match(realtime, /api\.realtimeLeaseStop\(serverAuthorityToken/);
  assert.doesNotMatch(realtime, /const serverLease = \{[\s\S]*authorityToken:/);
  assert.match(realtime, /api\.realtimeLeaseRenew\(sessionToken/);
  assert.match(realtime, /leaseToken: voiceLeaseTokenRef\.current/);
  assert.match(realtime, /leaseGeneration: voiceLeaseGenerationRef\.current/);
  assert.match(realtime, /const appStateRef = useRef\(AppState\.currentState\)/);
  assert.match(realtime, /const lifecycleStopLatchRef = useRef\(new PersonalRealtimeTerminalLatch\(\)\)/);
  assert.match(realtime, /AppState\.addEventListener\('change',[\s\S]*personalRealtimeAppLifecycleAction\(previousState, nextState, statusRef\.current\) === 'stop'[\s\S]*lifecycleStopLatchRef\.current\.run\(\(\) => stop\('cancelled'\)\)/);
  assert.match(realtime, /nextState === 'active'[\s\S]*lifecycleStopLatchRef\.current\.rearm\(\)/);
  assert.match(realtime, /for \(const call of calls\) \{[\s\S]*await handleToolCall\(call, connectionGeneration\)/);
  assert.equal((realtime.match(/type: 'response\.create'/g) || []).length, 1);
  assert.doesNotMatch(root, /toolTemplate|provider\s*:|model\s*:|authority\s*:/);
});

test('server-directed voice navigation never republishes ambient speech from the client', () => {
  const root = source('src', 'navigation', 'RootNavigator.tsx');
  const actions = root.slice(
    root.indexOf('const handleRealtimeActions = useCallback'),
    root.indexOf('const openPersonalRealtimeThread = useCallback'),
  );
  assert.match(actions, /actionType === 'open_chat_thread'/);
  assert.match(actions, /const threadId = String\(action\.threadId/);
  assert.match(actions, /navigationRef\.navigate\('Thread', \{[\s\S]*threadId/);
  assert.doesNotMatch(actions, /api\.|fetch\(|sendScoutMessage|sendMessage|publish|POST/);
});

test('launch requires current server qualification, then authenticated control, before microphone or provider setup', () => {
  const realtime = source('src', 'realtime', 'usePersonalRealtime.ts');
  const office = source('src', 'realtime', 'OfficeEventsContext.tsx');
  const authority = source('src', 'realtime', 'personalRealtimeStartAuthority.ts');
  const start = realtime.slice(realtime.indexOf('const start = useCallback'), realtime.indexOf('const stop = useCallback'));
  const qualificationIndex = start.indexOf('loadQualifiedClientConfig(true)');
  const waitIndex = start.indexOf('waitForOfficeControlChannel');
  assert.ok(qualificationIndex >= 0);
  assert.ok(waitIndex >= 0);
  assert.ok(qualificationIndex < waitIndex);
  assert.ok(qualificationIndex < start.indexOf("audioFocusRuntime.acquire('personal_realtime'"));
  assert.ok(qualificationIndex < start.indexOf('mediaDevices.getUserMedia'));
  assert.ok(waitIndex < start.indexOf("audioFocusRuntime.acquire('personal_realtime'"));
  assert.ok(waitIndex < start.indexOf('mediaDevices.getUserMedia'));
  assert.match(start, /Promise\.resolve\(qualifiedClientConfig\)/);
  assert.doesNotMatch(start, /api\.clientConfig/);
  assert.match(start, /connectionGeneration: generationRef\.current/);
  assert.match(start, /liveSessionToken: authorityTokenRef\.current/);
  assert.match(start, /qualifiedAuthorityToken: qualifiedAuthorityTokenRef\.current/);
  assert.match(start, /qualificationEpoch: qualificationEpochRef\.current/);
  assert.match(start, /authStorageGeneration: currentAuthStorageGeneration\(\)/);
  assert.match(authority, /live\.authStorageGeneration === snapshot\.authStorageGeneration/);
  assert.match(authority, /live\.qualifiedAuthorityToken === snapshot\.sessionToken/);
  assert.match(authority, /live\.qualificationEpoch === snapshot\.qualificationEpoch/);
  assert.equal(
    (start.match(/runPersonalRealtimeGuardedStage\(\{/g) || []).length,
    3,
    'qualification, control, and focus acquisition each require the exact authority generation',
  );
  assert.match(start, /retireStale: \(staleLease\) => releasePersonalRealtimeTerminalFocus\(/);
  assert.match(start, /if \(!startAuthorityIsCurrent\(\) \|\| !lease\.isCurrent\(\)\)[\s\S]*mediaDevices\.getUserMedia/);
  assert.match(start, /if \(!startAuthorityIsCurrent\(\) \|\| !lease\.isCurrent\(\)\)[\s\S]*BonfireMediaSession\.activateVideoMeeting/);
  assert.match(start, /statusRef\.current === 'connecting'/);
  assert.match(office, /timeoutMs = 6_000/);
  assert.match(start, /Scout voice could not reach its control channel\. Please try again\./);
});

test('server qualification is polled, foreground-refreshed, and hides or stops fail-closed', () => {
  const realtime = source('src', 'realtime', 'usePersonalRealtime.ts');
  const client = source('src', 'api', 'client.ts');
  const room = source('src', 'realtime', 'useNativeRoom.ts');
  assert.match(realtime, /privateRealtimeVoiceIsQualified\(clientConfig\)/);
  assert.match(realtime, /setInterval\([\s\S]*PERSONAL_REALTIME_QUALIFICATION_POLL_MS/);
  assert.match(realtime, /nextState === 'active'[\s\S]*loadQualifiedClientConfig\(true\)/);
  assert.match(realtime, /qualification === 'unqualified' && statusRef\.current !== 'idle'[\s\S]*stop\('cancelled'\)/);
  assert.match(realtime, /enabled: NATIVE_REALTIME_VOICE_ENABLED[\s\S]*qualification === 'qualified'[\s\S]*qualifiedAuthorityTokenRef\.current === sessionToken/);
  assert.match(realtime, /if \(authorityTokenRef\.current !== sessionToken\)[\s\S]*generationRef\.current \+= 1/);
  assert.match(realtime, /qualifiedAuthorityTokenRef\.current === authorityToken[\s\S]*qualificationEpochRef\.current \+= 1/);
  assert.match(realtime, /PERSONAL_REALTIME_LEASE_RENEW_MS = 10_000/);
  assert.match(realtime, /armLeaseExpiryWatchdog\(\{/);
  assert.match(realtime, /voiceLeaseRenewAbortControllerRef\.current/);
  assert.match(realtime, /timeoutMs: leaseTiming\.renewRequestTimeoutMs/);
  assert.match(realtime, /private session expired\. Tap to retry\.[\s\S]*false/);
  assert.match(client, /realtimeLeaseRenew\([\s\S]*options: \{ signal\?: AbortSignal; timeoutMs\?: number \}[\s\S]*signal: options\.signal,[\s\S]*timeoutMs: options\.timeoutMs/);
  assert.match(realtime, /setTearingDown\(true\)[\s\S]*releasePersonalRealtimeTerminalFocus[\s\S]*setLiveStatus\('idle'\)[\s\S]*setTearingDown\(false\)/);
  assert.match(room, /nativeClientConfigCache\.load\(sessionToken\)/);
});

test('automatic reconnect preserves the logical binding and is strictly bounded', () => {
  const realtime = source('src', 'realtime', 'usePersonalRealtime.ts');
  const office = source('src', 'realtime', 'OfficeEventsContext.tsx');
  assert.match(realtime, /PERSONAL_REALTIME_RECONNECT_DELAYS_MS = \[500, 1_500\]/);
  assert.match(realtime, /voiceSessionId: voiceSessionIdRef\.current/);
  assert.match(realtime, /threadId: voiceThreadIdRef\.current/);
  assert.match(realtime, /api\.realtimeOffer\([\s\S]*sessionToken,[\s\S]*localSDP,[\s\S]*voiceSessionId,[\s\S]*createConversationOperationId\(\),[\s\S]*\)/);
  assert.match(realtime, /answer\.transportRevision <= previousTransportRevision/);
  assert.match(realtime, /reconnecting && answer\.threadId !== expectedThreadId/);
  assert.match(realtime, /preserveLogicalBinding/);
  assert.match(realtime, /reason === 'forced_close'[\s\S]*control\.reconnectEligible/);
  assert.match(realtime, /reason === 'forced_close'[\s\S]*AppState\.currentState === 'active'/);
  assert.match(realtime, /cancelReconnect\(false\);[\s\S]*const lease = leaseRef\.current/);
  assert.match(office, /officeControlChannelSnapshot/);
  assert.match(office, /markOfficeControlDisconnected\(sessionToken, false\)/);
  assert.match(office, /officeControlListeners\.add\(check\)/);
});
