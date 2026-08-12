import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import path from 'node:path';

const root = path.resolve(__dirname, '../../..');
const screen = readFileSync(path.join(root, 'mobile/src/screens/CanvasScreen.tsx'), 'utf8');
const hook = readFileSync(path.join(root, 'mobile/src/canvas/useLiveLine.ts'), 'utf8');
const client = readFileSync(path.join(root, 'mobile/src/api/client.ts'), 'utf8');
const types = readFileSync(path.join(root, 'mobile/src/api/types.ts'), 'utf8');

test('native Home consumes one server-owned snapshot and does not rebuild client priority', () => {
  assert.match(client, /home\(sessionToken: string\): Promise<HomeResponse>/u);
  assert.match(client, /request<HomeResponse>\("\/assistant\/home"/u);
  assert.match(hook, /const response = await api\.home\(sessionToken\)/u);
  assert.doesNotMatch(hook, /Promise\.all|api\.rooms|api\.notifications|api\.scoutThreads|buildHomeContinuity/u);
  assert.match(hook, /setSnapshot\(EMPTY\)[\s\S]*\[401, 403\]/u);
  assert.match(hook, /catch \(error\)[\s\S]*setSnapshot\(EMPTY\)[\s\S]*setFreshness\('stale'\)/u);
  assert.doesNotMatch(hook, /MAX_STALE_HOME_MS|receivedAtRef/u);
  assert.match(hook, /const HOME_STARTER_IDS = \['continue', 'explore', 'create', 'challenge'\] as const/u);
  assert.match(hook, /snapshot\.version === 'home-v2'/u);
  assert.match(hook, /snapshot\.starters\.length === HOME_STARTER_IDS\.length/u);
  assert.match(types, /id: 'continue' \| 'explore' \| 'create' \| 'challenge';/u);
  assert.match(types, /version: 'home-v2';/u);
  assert.match(types, /detail: string;\s+suggestions: HomeStarterSuggestion\[\]/u);
  assert.match(types, /HomeStarterDestination =\s+\| \{ route: 'new-private' \}\s+\| \{ route: 'thread'; threadId: string;/u);
  assert.doesNotMatch(screen, /const HOME_STARTER_IDS|Tell me what you want to pick back up|Help me explore the most important/u);
});

test('native Home always renders server categories and expands an accessible suggestion surface', () => {
  assert.match(screen, /\{home\.starters\.length > 0 && !activeStarter \? \(/u);
  assert.match(screen, /home\.starters\.map\(\(starter\) =>/u);
  assert.match(screen, /onPress=\{\(\) => setActiveStarterID\(starter\.id\)\}/u);
  assert.match(screen, /accessibilityState=\{\{ expanded: activeStarterID === starter\.id \}\}/u);
  assert.match(screen, /\{activeStarter \? \(/u);
  assert.match(screen, /accessibilityLabel=\{`\$\{activeStarter\.label\} suggestions`\}/u);
  assert.match(screen, /accessibilityLabel="Back to starter categories"/u);
  assert.doesNotMatch(screen, /composerFocused|showHomeStarters|width >= 700/u);
});

test('native Home suggestions only seed the editable ordinary composer', () => {
  const starterStart = screen.indexOf('const useStarterSuggestion');
  const starterBody = screen.slice(starterStart, screen.indexOf('\n  return (', starterStart));
  assert.match(starterBody, /setDraft\(suggestion\.text\)/u);
  assert.match(starterBody, /setDraftDestination\(suggestion\.destination\)/u);
  assert.match(starterBody, /inputRef\.current\?\.focus/u);
  assert.doesNotMatch(starterBody, /api\.|sendOpening|navigate|tool|mode/u);
  assert.match(screen, /Nothing is sent until you press Send\./u);
  assert.match(screen, /onPress=\{\(\) => useStarterSuggestion\(suggestion\)\}/u);
});

test('Continue sends into the exact destination thread while arbitrary text opens a new private thread', () => {
  const sendStart = screen.indexOf('const sendOpening');
  const sendBody = screen.slice(sendStart, screen.indexOf('\n  // The disabled mic', sendStart));
  assert.match(sendBody, /if \(draftDestination\?\.route === 'thread'\)/u);
  assert.match(sendBody, /api\.sendScoutMessage\([\s\S]*draftDestination\.threadId/u);
  assert.match(sendBody, /navigation\.navigate\('Thread', \{\s+threadId: draftDestination\.threadId/u);
  assert.match(sendBody, /const attempt = homeScoutOpeningAttempt\(openingAttemptRef\.current, draft\)/u);
  assert.match(sendBody, /createThread: \(body, idempotencyKey\) => api\.createScoutThread/u);
  assert.ok(sendBody.indexOf("draftDestination?.route === 'thread'") < sendBody.indexOf('homeScoutOpeningAttempt'));
  assert.match(screen, /Send as a new private conversation instead/u);
});

test('native Home separates bounded dictation from circular realtime voice and exact destinations', () => {
  assert.match(screen, /useComposerDictation\(\{/u);
  assert.match(screen, /accessibilityLabel="Dictate a message"/u);
  assert.match(screen, /Start a new private voice chat with Scout/u);
  assert.match(screen, /SymbolView name="waveform"/u);
  assert.doesNotMatch(screen, /<StrideCradle/u);
  assert.match(screen, /destination\.route === 'alerts'/u);
  assert.match(screen, /destination\.route === 'room'/u);
  assert.match(screen, /messageId: destination\.messageId/u);
});

test('native Home shows a compact room jump only while a meeting is live', () => {
  assert.match(screen, /const liveMeeting = home\.continuity\.find\(\(item\) => item\.kind === 'live-meeting'\)/u);
  assert.match(screen, /const continuityItems = home\.continuity\.filter\(\(item\) => item\.kind !== 'live-meeting'\)/u);
  assert.match(screen, /\{liveMeeting \? \([\s\S]*openContinuity\(liveMeeting\)[\s\S]*styles\.liveMeetingJump/u);
  assert.doesNotMatch(screen, /all quiet|No active rooms|rooms are quiet/iu);
});

test('native Home expands truthful context and suggestions for accessibility text sizes', () => {
  assert.match(screen, /const \{ fontScale \} = useWindowDimensions\(\)/u);
  assert.match(screen, /const largeHomeType = fontScale >= 1\.5/u);
  assert.match(screen, /largeHomeType && styles\.starterLargeType/u);
  assert.match(screen, /homeCopyBlock: \{ width: '100%', minHeight: 0 \}/u);
  assert.match(screen, /maxWidth: '100%',[\s\S]*flexShrink: 1,[\s\S]*marginBottom/u);
  assert.match(screen, /keyboardVisible && styles\.keyboardSky/u);
  assert.match(screen, /Keyboard\.addListener\('keyboardDidShow'/u);
  assert.match(screen, /Keyboard\.addListener\('keyboardDidHide'/u);
  assert.match(screen, /accessibilityLabel=\{starter\.label\}/u);
  assert.doesNotMatch(screen, /style=\{styles\.starterText\}>\{starter\.detail\}/u);
  assert.match(screen, /accessibilityHint="Shows editable message suggestions\."/u);
  assert.doesNotMatch(screen, /numberOfLines=\{1\} style=\{styles\.continuity(?:Eyebrow|Title)\}/u);
  assert.doesNotMatch(screen, /numberOfLines=\{2\} style=\{styles\.continuityDetail\}/u);
});

test('native Home does not claim a Project model', () => {
  assert.doesNotMatch(screen, /projectId|project chip|createProject|Create private project/iu);
  assert.doesNotMatch(types, /HomeProject|HomeStarterProject/u);
});

test('native Home has no permanent voice-policy or legacy live-line copy', () => {
  assert.doesNotMatch(screen, /Voice is unavailable\. You can still message Scout\./u);
  assert.match(screen, /const voiceNotice = realtime\.error/u);
  assert.match(screen, /styles\.homeCopyBlock/u);
  assert.doesNotMatch(screen, /styles\.liveLine|styles\.liveText|styles\.liveAuthor/u);
});

test('native Home prompt is never treated as a credential or autofill field', () => {
  assert.match(screen, /accessibilityLabel="Message Scout from Home"[\s\S]*autoComplete="off"/u);
  assert.match(screen, /importantForAutofill="no"/u);
  assert.match(screen, /textContentType="none"/u);
});
