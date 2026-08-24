import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const mobileRoot = path.resolve(import.meta.dirname, '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('Work is a first-class destination in the five-item native shell', () => {
  const shell = source('src', 'navigation', 'nativeShellModel.ts');
  const root = source('src', 'navigation', 'RootNavigator.tsx');
  const types = source('src', 'navigation', 'types.ts');

  assert.match(shell, /Five first-class destinations — Home \/ Meet \/ Chat \/ Work \/ Files/u);
  assert.deepEqual(
    [...shell.matchAll(/\{ id: '(home|video|chat|work|files)', label: '(Home|Meet|Chat|Work|Files)'/gu)].map((match) => match[1]),
    ['home', 'video', 'chat', 'work', 'files'],
  );
  assert.match(root, /name="WorkHome" component=\{WorkHubScreen\}/u);
  assert.match(root, /name="Board" component=\{WorkHubScreen\}/u);
  assert.match(types, /WorkHome: \{ projectId\?: string; rootRunId\?: string \} \| undefined/u);
  assert.match(shell, /\{ id: 'work', label: 'Work', route: 'WorkHome', icon: 'work-pencil' \}/u);
});

test('the Work hub is one filtered, virtualized project library with calm sections', () => {
  const hub = source('src', 'screens', 'WorkHubScreen.tsx');
  const model = source('src', 'work', 'studioProjectModel.ts');

  assert.match(hub, /api\.studioProjects\(sessionToken, \{ limit: 100 \}\)/u);
  assert.match(hub, /<FlashList/u);
  assert.match(model, /\{ id: 'all', label: 'All' \}/u);
  assert.match(model, /\{ id: 'presentation', label: 'Presentations' \}/u);
  assert.match(model, /\{ id: 'document', label: 'Research' \}/u);
  assert.match(model, /case 'needs-you': return 'Needs you'/u);
  assert.match(model, /case 'needs-attention': return 'Needs attention'/u);
  assert.match(model, /case 'in-progress': return 'In progress'/u);
  assert.match(model, /case 'recent': return 'Recent'/u);
  assert.doesNotMatch(hub, /BoardScreen|WorkHomeScreen/u);
});

test('iPhone uses one swipe-dismissable project sheet while iPad keeps list and detail together', () => {
  const hub = source('src', 'screens', 'WorkHubScreen.tsx');
  const sheet = source('src', 'work', 'WorkProjectSheet.tsx');

  assert.match(hub, /const WORK_HUB_SPLIT_WIDTH = 744/u);
  assert.match(hub, /split \? \([\s\S]*<WorkProjectDetail/u);
  assert.match(hub, /\) : \([\s\S]*<WorkProjectSheet/u);
  assert.match(sheet, /presentationStyle="formSheet"/u);
  assert.match(sheet, /allowSwipeDismissal/u);
  assert.match(sheet, /SCOUT NEEDS YOUR CALL/u);
	assert.match(sheet, /const hasSource = Boolean/u);
	assert.match(sheet, /studioProjectAttentionCopy\(project\.attention, hasSource\)/u);
	assert.match(sheet, /accessibilityRole="alert"[\s\S]*attention\.title[\s\S]*attention\.body/u);
	assert.match(sheet, /attention \? null : \(/u);
	assert.match(sheet, /accessibilityLabel=\{attention\?\.actionLabel \|\| 'Open source conversation'\}/u);
	assert.match(sheet, /\{attention\?\.actionLabel \|\| 'Source conversation'\}/u);
  assert.match(sheet, /You can keep working elsewhere; Scout will update this project here/u);
  assert.match(sheet, /Full slide editing is available on desktop/u);
	assert.match(sheet, /'Review changes'/u);
	assert.match(sheet, /'Continue review'/u);
	assert.match(sheet, /studioProjectResultIsFinal\(project\)/u);
	assert.match(hub, /api\.reviewEditedGoal\(sessionToken, project\.rootArtifactId, project\.result\.artifactId, project\.result\.version, project\.result\.digest\)/u);
	assert.match(hub, /api\.resumeGoal\(sessionToken, project\.rootArtifactId\)/u);
});

test('project results open exact native deck or document destinations and never raw JSON', () => {
  const hub = source('src', 'screens', 'WorkHubScreen.tsx');
  const model = source('src', 'work', 'studioProjectModel.ts');

  assert.match(model, /exactDigest\.test\(artifactDigest\)/u);
  assert.match(model, /if \(studioKind !== 'deck'\) return null/u);
  assert.match(model, /canPresent: result\.canPresent === true/u);
  assert.match(hub, /navigation\.navigate\('DeckViewer', \{[\s\S]*artifactVersion: target\.artifactVersion,[\s\S]*artifactDigest: target\.artifactDigest/u);
  assert.match(hub, /previewOnly: !target\.canPresent/u);
  assert.match(hub, /artifactStudioPath\([\s\S]*target\.artifactId,[\s\S]*'document',[\s\S]*version: target\.artifactVersion, digest: target\.artifactDigest/u);
  assert.match(hub, /if \(!split\) setSheetVisible\(false\);[\s\S]*navigation\.navigate\('DeckViewer'/u);
  assert.doesNotMatch(hub, /JSON\.stringify\(project/u);
});

test('focused Work invalidates and polls live projects while action failures stay visible in the sheet', () => {
  const hub = source('src', 'screens', 'WorkHubScreen.tsx');
  const sheet = source('src', 'work', 'WorkProjectSheet.tsx');

  assert.match(hub, /\['chat_thread', 'file', 'memory', 'action'\]/u);
  assert.match(hub, /setInterval\(\(\) => \{ void load\(false, true\); \}, 6_000\)/u);
  assert.match(hub, /const version = \+\+requestVersionRef\.current/u);
  assert.match(hub, /caught instanceof BonfireApiError && caught\.status === 409[\s\S]*api\.studioProject\(sessionToken, project\.id\)/u);
  assert.match(hub, /<WorkProjectSheet[\s\S]*actionError=\{actionError\}/u);
  assert.match(sheet, /accessibilityRole="alert"[\s\S]*\{actionError\}/u);
});

test('Home continuity and chat receipts hand off quietly to the same Work project', () => {
  const canvas = source('src', 'screens', 'CanvasScreen.tsx');
  const thread = source('src', 'screens', 'ThreadScreen.tsx');
  const bubble = source('src', 'messaging', 'MessageBubble.tsx');

  assert.match(canvas, /navigation\.navigate\('WorkHome', \{ rootRunId: item\.workId \}\)/u);
  assert.match(thread, /navigation\.navigate\('WorkHome', \{ projectId: normalizedId \}\)/u);
  assert.match(thread, /currentWorkMessage && !currentWorkMessage\.studioProject && showCurrentWorkActivity/u);
  assert.match(bubble, /const studioProject = message\.studioProject/u);
  assert.match(bubble, /accessibilityHint="Opens this exact request in Work"/u);
	assert.match(bubble, />View in Work</u);
	assert.match(bubble, /studioProjectBoundedProgress\(studioProject\.progressPercent\)/u);
	assert.match(bubble, /studioProject\.checkpoint/u);
	assert.match(bubble, /onResolveWorkCheckpoint\?\.\(message, option\)/u);
  assert.doesNotMatch(bubble, /JSON\.stringify\(studioProject/u);
});

test('an exact Work target is consumed only after success or authoritative unavailability', () => {
  const hub = source('src', 'screens', 'WorkHubScreen.tsx');

  assert.match(hub, /const attemptedRouteProjectRef = useRef\(''\)/u);
	assert.match(hub, /const \[routeError, setRouteError\] = useState\(''\)/u);
  assert.match(hub, /const attemptKey = `\$\{requestKey\}:\$\{routeRetryVersion\}`/u);
  assert.match(hub, /\.then\(\(response\) => \{[\s\S]*handledRouteProjectRef\.current = requestKey[\s\S]*navigation\.setParams/u);
  assert.match(hub, /caught instanceof BonfireApiError && \(caught\.status === 403 \|\| caught\.status === 404\)[\s\S]*navigation\.setParams/u);
  assert.match(hub, /setRouteRetryVersion\(\(version\) => version \+ 1\)/u);
	assert.match(hub, /\{routeError \|\| error \? \(/u);
  assert.doesNotMatch(hub, /\.catch\([^)]*\)[\s\S]{0,120}\.finally\(\(\) => \{[\s\S]*navigation\.setParams/u);
});
