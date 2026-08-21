import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import {
  NATIVE_SHELL_SIDEBAR_MIN_WIDTH,
  NATIVE_SHELL_SIDEBAR_MAX_FONT_SCALE,
  createNativeShellSelectionCoordinator,
  nativeShellDestinationForRoute,
  nativeShellDestinations,
  nativeShellLayout,
  nativeShellSelectionAnnouncement,
  nativeShellVisibleForRoute,
} from '../navigation/nativeShellModel';
import { registerTestStubModules } from './support/registerTestStubModules';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('the universal IA is exact and ordered', () => {
  assert.deepEqual(nativeShellDestinations.map(({ id, label }) => ({ id, label })), [
    { id: 'home', label: 'Home' },
    { id: 'video', label: 'Meet' },
    { id: 'chat', label: 'Chat' },
    { id: 'files', label: 'Files' },
  ]);
});

test('available width, including orientation and split view resizing, selects composition', () => {
  assert.equal(nativeShellLayout(390), 'compact');
  assert.equal(nativeShellLayout(430), 'compact');
  assert.equal(nativeShellLayout(NATIVE_SHELL_SIDEBAR_MIN_WIDTH - 1), 'compact');
  assert.equal(nativeShellLayout(NATIVE_SHELL_SIDEBAR_MIN_WIDTH), 'sidebar');
  assert.equal(nativeShellLayout(1024), 'sidebar');
  assert.equal(nativeShellLayout(1024, true, NATIVE_SHELL_SIDEBAR_MAX_FONT_SCALE - 0.01), 'sidebar');
  assert.equal(nativeShellLayout(1024, true, NATIVE_SHELL_SIDEBAR_MAX_FONT_SCALE), 'compact');
  assert.equal(nativeShellLayout(874, false), 'compact');
  assert.equal(nativeShellLayout(Number.NaN), 'compact');
});

test('selection feedback is deterministic and bounded', () => {
  assert.equal(nativeShellSelectionAnnouncement('home'), 'Home selected');
  assert.equal(nativeShellSelectionAnnouncement('video'), 'Meet selected');
  assert.equal(nativeShellSelectionAnnouncement('chat'), 'Chat selected');
  assert.equal(nativeShellSelectionAnnouncement('files'), 'Files selected');
});

test('mounted shell press preserves its navigator child through destination, full-screen, and width changes', async () => {
  const testGlobal = globalThis as typeof globalThis & {
    __nativeShellWidth?: number;
    __nativeShellInsets?: { top: number; right: number; bottom: number; left: number };
    __nativeShellFontScale?: number;
    __nativeShellAnnouncements?: string[];
  };
  registerTestStubModules('native-shell-stub:', {
        'native-shell-stub:react-native': `export const Pressable='Pressable'; export const Text='Text'; export const View='View'; export const StyleSheet={create:value=>value,hairlineWidth:1,absoluteFill:{},absoluteFillObject:{}}; export const Platform={OS:'ios',isPad:true}; export const DynamicColorIOS=value=>({dynamic:value}); export const Easing={bezier:()=>()=>0}; export const AccessibilityInfo={announceForAccessibility:message=>globalThis.__nativeShellAnnouncements.push(message),isReduceTransparencyEnabled:async()=>false,isReduceMotionEnabled:async()=>false,addEventListener:()=>({remove(){}})}; export const useWindowDimensions=()=>({width:globalThis.__nativeShellWidth||390,height:844,fontScale:globalThis.__nativeShellFontScale||1});`,
        'native-shell-stub:react-native-svg': `const Svg='Svg'; export default Svg; export const Path='Path'; export const Circle='Circle'; export const Ellipse='Ellipse';`,
        'native-shell-stub:expo-symbols': `export const SymbolView='SymbolView';`,
        'native-shell-stub:react-native-safe-area-context': `export const useSafeAreaInsets=()=>globalThis.__nativeShellInsets||({top:0,right:0,bottom:0,left:0});`,
        'native-shell-stub:expo-blur': `export const BlurView='BlurView';`,
        'native-shell-stub:expo-glass-effect': `export const GlassView='GlassView'; export const isLiquidGlassAvailable=()=>false;`,
  });
  testGlobal.__nativeShellWidth = 390;
  testGlobal.__nativeShellFontScale = 1;
  testGlobal.__nativeShellInsets = { top: 47, right: 0, bottom: 34, left: 0 };
  testGlobal.__nativeShellAnnouncements = [];
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const React = (await import('react')).default;
  const { act, create } = await import('react-test-renderer');
  const { NativeUniversalShell } = await import('../navigation/NativeUniversalShell');

  let pushThread = () => {};
  let editDraft = (_value: string) => {};
  let navigationMountCount = 0;
  function NavigationContainerProbe() {
    const [mountID] = React.useState(() => `navigation-${++navigationMountCount}`);
    const [stack, setStack] = React.useState(['Canvas']);
    const [threadDraft, setThreadDraft] = React.useState('preserved');
    pushThread = () => setStack(current => [...current, 'Thread']);
    editDraft = value => setThreadDraft(value);
    return React.createElement('NavigationContainer', {
      testID: 'root-navigation',
      mountID,
      stack: stack.join(','),
      threadDraft,
    });
  }

  const navigator = React.createElement(NavigationContainerProbe);
  let requestedRoute: string | undefined;
  let requestedParams: unknown;
  const coordinator = createNativeShellSelectionCoordinator(message => testGlobal.__nativeShellAnnouncements?.push(message));
  const onSelect = (destination: (typeof nativeShellDestinations)[number]) => coordinator.select(destination, (route, params) => { requestedRoute = route; requestedParams = params; });
  const shell = (active: Parameters<typeof NativeUniversalShell>[0]['active'], visible: boolean, access: 'core' | 'full' = 'full') =>
    React.createElement(NativeUniversalShell, { active, access, visible, onSelect, children: navigator });

  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => { renderer = create(shell('home', true)); });
  const mountedNavigator = renderer!.root.findByProps({ testID: 'root-navigation' });
  const mountID = mountedNavigator.props.mountID;
  const pathType = 'Path' as unknown as React.ElementType;
  const ellipseType = 'Ellipse' as unknown as React.ElementType;
  const renderedPaths = renderer!.root.findAllByType(pathType);
  const renderedEllipses = renderer!.root.findAllByType(ellipseType);
  assert.equal(renderedPaths[0].props.stroke, '#FF5A19', 'selected custom mark lost its static ember stroke');
  assert.deepEqual(
    renderedEllipses[0].props.stroke,
    { dynamic: { light: '#26231E', dark: '#F7F7F9' } },
    'inactive custom mark did not preserve the native adaptive ColorValue',
  );
  assert.notEqual(renderedEllipses[0].props.stroke, '[object Object]', 'adaptive SVG stroke was string-coerced into an invalid color');

  // Test Chat navigation (replaces Work test since Work is no longer a destination)
  const chatPressable = renderer!.root.findByProps({ accessibilityLabel: 'Chat' });
  await act(async () => { chatPressable.props.onPress(); });
  assert.equal(requestedRoute, 'Chat');
  assert.deepEqual(testGlobal.__nativeShellAnnouncements, [], 'selection announced before navigation committed');
  const selected = coordinator.commit('Chat');
  assert.equal(selected, 'chat');
  assert.deepEqual(testGlobal.__nativeShellAnnouncements, ['Chat selected']);

  const videoPressable = renderer!.root.findByProps({ accessibilityLabel: 'Meet' });
  await act(async () => { videoPressable.props.onPress(); });
  assert.equal(requestedRoute, 'Meet');
  assert.equal(requestedParams, undefined);

  await act(async () => { renderer!.update(shell(selected, true)); });
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }), mountedNavigator);
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.mountID, mountID);
  assert.deepEqual(renderer!.root.findByProps({ accessibilityLabel: 'Chat' }).props.accessibilityState, { selected: true });

  // With 4-destination model, both core and full access show exactly 4 tabs
  await act(async () => { renderer!.update(shell('home', true, 'core')); });
  assert.equal(renderer!.root.findAllByProps({ accessibilityRole: 'tab' }).length, 4);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Home' }).length, 1);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Meet' }).length, 1);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Chat' }).length, 1);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Files' }).length, 1);

  await act(async () => { renderer!.update(shell('home', true)); });

  await act(async () => {
    pushThread();
    editDraft('reply in progress');
  });
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.stack, 'Canvas,Thread');
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.threadDraft, 'reply in progress');
  await act(async () => { renderer!.update(shell(coordinator.commit('Thread'), false)); });
  assert.equal(coordinator.current(), 'chat', 'full-screen route lost its owning destination');
  assert.deepEqual(testGlobal.__nativeShellAnnouncements, ['Chat selected'], 'hidden full-screen route emitted a false destination announcement');
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }), mountedNavigator);
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.mountID, mountID);
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.stack, 'Canvas,Thread');
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.threadDraft, 'reply in progress');
  assert.equal(renderer!.root.findAllByProps({ accessibilityRole: 'tablist' }).length, 0);

  testGlobal.__nativeShellWidth = 1024;
  await act(async () => { renderer!.update(shell(coordinator.commit('Chat'), true)); });
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }), mountedNavigator);
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.mountID, mountID);
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.stack, 'Canvas,Thread');
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.threadDraft, 'reply in progress');
  assert.equal(renderer!.root.findAllByProps({ accessibilityRole: 'tablist' }).length, 1, 'sidebar tablist did not mount after resize');
  assert.deepEqual(testGlobal.__nativeShellAnnouncements, ['Chat selected'], 'resize or owner restore announced a false destination');
  await act(async () => { renderer!.unmount(); });
});

test('deep destinations preserve their owning top-level context', () => {
  // With the 4-destination model, legacy destinations like 'work' now route to 'home'
  const coordinator = createNativeShellSelectionCoordinator(() => {}, 'home');
  assert.equal(coordinator.commit('Thread'), 'home');
  // Legacy Work, Network, WorkSearch, You routes now resolve to 'home'
  assert.equal(nativeShellDestinationForRoute('Board'), 'home');
  assert.equal(nativeShellDestinationForRoute('Meet'), 'video');
  assert.equal(nativeShellDestinationForRoute('Chat'), 'chat');
  assert.equal(nativeShellDestinationForRoute('Deck'), 'home');
  assert.equal(nativeShellDestinationForRoute('Deck', { segment: 'threads' }), 'chat');
  assert.equal(nativeShellDestinationForRoute('Deck', { segment: 'rooms' }), 'video');
  assert.equal(nativeShellDestinationForRoute('Deck', { segment: 'work' }), 'home');
  assert.equal(nativeShellDestinationForRoute('Meetings'), 'home');
  assert.equal(nativeShellDestinationForRoute('Files'), 'files');
  // Legacy network/work-search/you destinations now route to 'home'
  assert.equal(nativeShellDestinationForRoute('NetworkPreview'), 'home');
  assert.equal(nativeShellDestinationForRoute('ContactInbox'), 'home');
  assert.equal(nativeShellDestinationForRoute('WorkRecord'), 'home');
});

test('focused flows hide compact chrome while iPad threads may retain only the sidebar', () => {
  for (const route of ['Login', 'Thread', 'Room', 'CreateRoom', 'NewConversation', 'OSWeb'] as const) {
    assert.equal(nativeShellVisibleForRoute(route), false, route);
  }
  assert.equal(nativeShellVisibleForRoute('Canvas'), true);
  assert.equal(nativeShellVisibleForRoute('NetworkHome'), true);
  const root = source('src', 'navigation', 'RootNavigator.tsx');
  // iPad workstation: Thread/ChannelRiff always keep sidebar, NewConversation/CreateRoom keep at ≥1024
  assert.match(root, /keepSidebarForFocusedRoute=\{Boolean\(user && sessionToken && \(/);
  assert.match(root, /activeRoute === 'Thread'/);
  assert.match(root, /activeRoute === 'ChannelRiff'/);
  assert.match(root, /isWorkstationWidth && \(activeRoute === 'NewConversation' \|\| activeRoute === 'CreateRoom'\)/);
  assert.match(root, /WORKSTATION_MIN_WIDTH = 1024/);
  assert.match(root, /presentation: 'card'/);
  assert.doesNotMatch(root, /presentation: 'fullScreenModal'/);
});

test('compact and iPad compositions are accessible, touch-safe, and resize-driven', () => {
  const shell = source('src', 'navigation', 'NativeUniversalShell.tsx');
  assert.match(shell, /useWindowDimensions\(\)/);
  assert.match(shell, /nativeShellLayout\(width, Platform\.OS !== 'ios' \|\| Platform\.isPad, fontScale\)/);
  assert.match(shell, /accessibilityRole="tablist"/);
  assert.match(shell, /accessibilityRole="tab"/);
  assert.match(shell, /accessibilityState=\{\{ selected \}\}/);
  assert.match(shell, /minHeight: 48/);
  assert.match(shell, /minHeight: 58/);
  assert.equal((shell.match(/style=\{styles\.destMark\}/g) ?? []).length, 5);
  assert.match(shell, /<Text accessibilityRole="header" style=\{styles\.sidebarWordmark\}>stride<\/Text>/);
  assert.match(shell, /color=\{selected \? colors\.ember : colors\.text1\}/);
  assert.doesNotMatch(shell, /String\(selected \? colors\.ember : colors\.text1\)/);
  assert.match(shell, /compactItem:[\s\S]*zIndex: 1/);
  assert.match(shell, /iconWrap: \{ position: 'relative', opacity: 1, zIndex: 2 \}/);
  assert.match(shell, /destMark: \{ opacity: 1, zIndex: 2 \}/);
  assert.match(shell, /transform: \[\{ scale: 0\.96 \}\]/);
  // Slim rail design uses icon-only marks without labels, no text scaling needed
  assert.doesNotMatch(shell, /Animated\.|LayoutAnimation|setTimeout/);
  assert.equal((shell.match(/\{children\}/g) ?? []).length, 1);
  assert.doesNotMatch(shell, /if \(!visible\) return/);
  assert.match(shell, /<View style=\{\[styles\.content, sidebar && styles\.contentSidebar, compact && styles\.contentCompact\]\}>\s*\{children\}/);
});

test('parent-off surfaces are opaque and do not mount W5, W6, or PN child components', () => {
  const screens = source('src', 'screens', 'NativeShellScreens.tsx');
  assert.match(screens, /W6 qualification is off/);
  assert.match(screens, /Interpretation, retrieval, contact, and result components are not mounted/);
  assert.match(screens, /W5 custody is off/);
  assert.match(screens, /No private MyMind child, body, or reconstructed history is mounted/);
  assert.match(screens, /Preview, publication, blocks, workstream, moderation, and public presence are parent-off/);
  assert.doesNotMatch(screens, /NetworkSearchScreen|ContactInboxScreen|MyMindScreen|PublicWorkspaceView|WorkstreamRow/);
});

test('private network preparation stays distinct from parent-off publication and discovery', () => {
  const screens = source('src', 'screens', 'NativeShellScreens.tsx');
  assert.match(screens, /route: 'NetworkDraft'/);
  assert.doesNotMatch(screens, /route: 'NetworkPreview'|route: 'NetworkBlocks'/);
  assert.match(screens, /A private draft is available\. Every public network path remains off/);
});

test('navigation integration keeps push/deep-link handling and latest-thread routes intact', () => {
  const root = source('src', 'navigation', 'RootNavigator.tsx');
  assert.match(root, /usePushRegistration\(\{/);
  assert.match(root, /navigationRef\.navigate\('Thread'/);
  assert.match(root, /onStateChange=\{syncActiveRoute\}/);
  assert.match(root, /shellSelectionRef\.current\.select\(destination/);
  assert.match(root, /navigationRef\.navigate\(\{ name: route, params \} as never\)/);
  assert.doesNotMatch(root, /navigationRef\.resetRoot/);
  assert.match(root, /createNativeShellSelectionCoordinator/);
  assert.match(root, /shellSelectionRef\.current\.commit\(route, currentRoute\?\.params\)/);
  assert.match(root, /<Stack\.Screen[\s\S]*?name="Thread"[\s\S]*?component=\{ThreadScreen\}/);
  assert.match(root, /<Stack\.Screen name="Room" component=\{RoomScreen\}/);
  assert.match(root, /name="NetworkSearch" component=\{WorkSearchHomeScreen\}/);
  assert.match(root, /name="ContactInbox" component=\{WorkSearchHomeScreen\}/);
  assert.match(root, /name="NetworkRecruiterView" component=\{NetworkHomeScreen\}/);
  assert.match(root, /name="NetworkPreview" component=\{NetworkHomeScreen\}/);
  assert.match(root, /name="NetworkBlocks" component=\{NetworkHomeScreen\}/);
});

test('shell destination lists handle keyboard dismissal and offline/off copy without fake data', () => {
  const screens = source('src', 'screens', 'NativeShellScreens.tsx');
	const deck = source('src', 'screens', 'DeckScreen.tsx');
	const room = source('src', 'screens', 'RoomScreen.tsx');
	const root = source('src', 'navigation', 'RootNavigator.tsx');
  assert.match(screens, /keyboardDismissMode="on-drag"/);
  assert.match(screens, /keyboardShouldPersistTaps="handled"/);
  assert.match(screens, /No public child surface or fixture data is mounted/);
  assert.doesNotMatch(screens, /Northstar|Bonfire|Alex|fictional_/);
	assert.doesNotMatch(screens, /route: 'Board', label: 'Board'/);
	assert.doesNotMatch(deck, /route: 'Board', label: 'Board'/);
	assert.doesNotMatch(room, /id: 'board', label: 'Board'/);
	assert.match(root, /tool === 'board'\) navigationRef\.navigate\('WorkHome'\)/u);
});
