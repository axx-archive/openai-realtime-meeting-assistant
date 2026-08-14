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

test('floating voice control exposes truthful state and opens only the exact bound thread', () => {
  const root = source('src', 'navigation', 'RootNavigator.tsx');
  const shell = source('src', 'navigation', 'NativeUniversalShell.tsx');
  const control = source('src', 'realtime', 'PersonalRealtimeFloatingControl.tsx');
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
  assert.match(root, /personalRealtimeVisible=\{Boolean\(user && sessionToken && activeRoute !== 'Room'\)\}/);
  assert.match(root, /roomActive=\{activeRoute === 'Room'\}/);
  assert.match(shell, /\(personalRealtimeVisible \|\| realtime\?\.active\) && realtime/);
  assert.match(source('src', 'realtime', 'PersonalRealtimeProvider.tsx'), /if \(roomActive && realtime\.active\) void realtime\.stop\('cancelled'\)/);
  assert.match(root, /navigationRef\.navigate\('Thread', \{ threadId, title: 'Scout voice' \}\)/);
  assert.match(shell, /onOpenThread=\{onOpenPersonalRealtimeThread\}/);
  assert.match(control, /connecting: 'Connecting'/);
  assert.match(control, /listening: 'Listening'/);
  assert.match(control, /hearing: 'Hearing you'/);
  assert.match(control, /thinking: 'Thinking'/);
  assert.match(control, /talking: 'Talking'/);
  assert.match(control, /acting: 'Acting'/);
  assert.match(control, /error: 'Needs attention'/);
  assert.match(control, /if \(realtime\.threadId\) onOpenThread\?\.\(realtime\.threadId\)/);
  assert.match(control, /<Waveform/);
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
  assert.match(realtime, /api\.realtimeOffer\(sessionToken, localSDP, voiceSessionId\)/);
  assert.match(realtime, /voiceThreadIdRef\.current/);
  assert.doesNotMatch(root, /toolTemplate|provider\s*:|model\s*:|authority\s*:/);
});

test('launch waits for authenticated control before microphone or provider setup', () => {
  const realtime = source('src', 'realtime', 'usePersonalRealtime.ts');
  const office = source('src', 'realtime', 'OfficeEventsContext.tsx');
  const start = realtime.slice(realtime.indexOf('const start = useCallback'), realtime.indexOf('const stop = useCallback'));
  const waitIndex = start.indexOf('await waitForOfficeControlChannel');
  assert.ok(waitIndex >= 0);
  assert.ok(waitIndex < start.indexOf("audioFocusRuntime.acquire('personal_realtime'"));
  assert.ok(waitIndex < start.indexOf('mediaDevices.getUserMedia'));
  assert.match(start, /generationRef\.current === connectionGeneration/);
  assert.match(start, /authorityTokenRef\.current === sessionToken/);
  assert.match(start, /statusRef\.current === 'connecting'/);
  assert.match(office, /timeoutMs = 6_000/);
  assert.match(start, /Scout voice could not reach its control channel\. Please try again\./);
});

test('automatic reconnect preserves the logical binding and is strictly bounded', () => {
  const realtime = source('src', 'realtime', 'usePersonalRealtime.ts');
  const office = source('src', 'realtime', 'OfficeEventsContext.tsx');
  assert.match(realtime, /PERSONAL_REALTIME_RECONNECT_DELAYS_MS = \[500, 1_500\]/);
  assert.match(realtime, /voiceSessionId: voiceSessionIdRef\.current/);
  assert.match(realtime, /threadId: voiceThreadIdRef\.current/);
  assert.match(realtime, /api\.realtimeOffer\(sessionToken, localSDP, voiceSessionId\)/);
  assert.match(realtime, /answer\.transportRevision <= previousTransportRevision/);
  assert.match(realtime, /reconnecting && answer\.threadId !== expectedThreadId/);
  assert.match(realtime, /preserveLogicalBinding/);
  assert.match(realtime, /reason === 'forced_close'[\s\S]*control\.reconnectEligible/);
  assert.match(realtime, /reason === 'forced_close'[\s\S]*AppState\.currentState === 'active'/);
  assert.match(realtime, /cancelReconnect\(\);[\s\S]*const lease = leaseRef\.current/);
  assert.match(office, /officeControlChannelSnapshot/);
  assert.match(office, /markOfficeControlDisconnected\(sessionToken, false\)/);
  assert.match(office, /officeControlListeners\.add\(check\)/);
});
