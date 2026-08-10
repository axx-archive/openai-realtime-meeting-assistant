import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { registerHooks } from 'node:module';
import { fileURLToPath } from 'node:url';
import {
  NATIVE_SHELL_SIDEBAR_MIN_WIDTH,
  createNativeShellSelectionCoordinator,
  nativeShellDestinationForRoute,
  nativeShellDestinations,
  nativeShellLayout,
  nativeShellSelectionAnnouncement,
  nativeShellVisibleForRoute,
} from '../navigation/nativeShellModel';

const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const source = (...parts: string[]) => fs.readFileSync(path.join(mobileRoot, ...parts), 'utf8');

test('the universal IA is exact and ordered', () => {
  assert.deepEqual(nativeShellDestinations.map(({ id, label }) => ({ id, label })), [
    { id: 'home', label: 'Home' },
    { id: 'work', label: 'Work' },
    { id: 'network', label: 'Network' },
    { id: 'work-search', label: 'Work Search' },
    { id: 'you', label: 'You' },
  ]);
});

test('available width, including orientation and split view resizing, selects composition', () => {
  assert.equal(nativeShellLayout(390), 'compact');
  assert.equal(nativeShellLayout(430), 'compact');
  assert.equal(nativeShellLayout(NATIVE_SHELL_SIDEBAR_MIN_WIDTH - 1), 'compact');
  assert.equal(nativeShellLayout(NATIVE_SHELL_SIDEBAR_MIN_WIDTH), 'sidebar');
  assert.equal(nativeShellLayout(1024), 'sidebar');
  assert.equal(nativeShellLayout(Number.NaN), 'compact');
});

test('selection feedback is deterministic and bounded', () => {
  assert.equal(nativeShellSelectionAnnouncement('home'), 'Home selected');
  assert.equal(nativeShellSelectionAnnouncement('work-search'), 'Work Search selected');
});

test('mounted shell press preserves its navigator child through destination, full-screen, and width changes', async () => {
  const testGlobal = globalThis as typeof globalThis & {
    __nativeShellWidth?: number;
    __nativeShellInsets?: { top: number; right: number; bottom: number; left: number };
    __nativeShellAnnouncements?: string[];
  };
  registerHooks({
    resolve(specifier, context, nextResolve) {
      if (['react-native', 'expo-symbols', 'react-native-safe-area-context'].includes(specifier)) {
        return { url: `native-shell-stub:${specifier}`, shortCircuit: true };
      }
      return nextResolve(specifier, context);
    },
    load(url, context, nextLoad) {
      const modules: Record<string, string> = {
        'native-shell-stub:react-native': `export const Pressable='Pressable'; export const Text='Text'; export const View='View'; export const StyleSheet={create:value=>value,hairlineWidth:1}; export const Platform={OS:'ios'}; export const DynamicColorIOS=value=>value.light; export const AccessibilityInfo={announceForAccessibility:message=>globalThis.__nativeShellAnnouncements.push(message)}; export const useWindowDimensions=()=>({width:globalThis.__nativeShellWidth||390,height:844});`,
        'native-shell-stub:expo-symbols': `export const SymbolView='SymbolView';`,
        'native-shell-stub:react-native-safe-area-context': `export const useSafeAreaInsets=()=>globalThis.__nativeShellInsets||({top:0,right:0,bottom:0,left:0});`,
      };
      if (modules[url]) return { format: 'module', source: modules[url], shortCircuit: true };
      return nextLoad(url, context);
    },
  });
  testGlobal.__nativeShellWidth = 390;
  testGlobal.__nativeShellInsets = { top: 47, right: 0, bottom: 34, left: 0 };
  testGlobal.__nativeShellAnnouncements = [];
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const React = (await import('react')).default;
  const { act, create } = await import('react-test-renderer');
  const { AccessibilityInfo } = await import('react-native');
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
  const coordinator = createNativeShellSelectionCoordinator(message => AccessibilityInfo.announceForAccessibility(message));
  const onSelect = (destination: (typeof nativeShellDestinations)[number]) => coordinator.select(destination, route => { requestedRoute = route });
  const shell = (active: Parameters<typeof NativeUniversalShell>[0]['active'], visible: boolean) =>
    React.createElement(NativeUniversalShell, { active, visible, onSelect, children: navigator });

  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => { renderer = create(shell('home', true)); });
  const mountedNavigator = renderer!.root.findByProps({ testID: 'root-navigation' });
  const mountID = mountedNavigator.props.mountID;
  const workPressable = renderer!.root.findByProps({ accessibilityLabel: 'Work' });
  await act(async () => { workPressable.props.onPress(); });
  assert.equal(requestedRoute, 'WorkHome');
  assert.deepEqual(testGlobal.__nativeShellAnnouncements, [], 'selection announced before navigation committed');
  const selected = coordinator.commit('WorkHome');
  assert.equal(selected, 'work');
  assert.deepEqual(testGlobal.__nativeShellAnnouncements, ['Work selected']);

  await act(async () => { renderer!.update(shell(selected, true)); });
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }), mountedNavigator);
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.mountID, mountID);
  assert.deepEqual(renderer!.root.findByProps({ accessibilityLabel: 'Work' }).props.accessibilityState, { selected: true });

  await act(async () => {
    pushThread();
    editDraft('reply in progress');
  });
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.stack, 'Canvas,Thread');
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.threadDraft, 'reply in progress');
  await act(async () => { renderer!.update(shell(coordinator.commit('Thread'), false)); });
  assert.equal(coordinator.current(), 'work', 'full-screen route lost its owning destination');
  assert.deepEqual(testGlobal.__nativeShellAnnouncements, ['Work selected'], 'hidden full-screen route emitted a false destination announcement');
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }), mountedNavigator);
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.mountID, mountID);
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.stack, 'Canvas,Thread');
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.threadDraft, 'reply in progress');
  assert.equal(renderer!.root.findAllByProps({ accessibilityRole: 'tablist' }).length, 0);

  testGlobal.__nativeShellWidth = 1024;
  await act(async () => { renderer!.update(shell(coordinator.commit('WorkHome'), true)); });
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }), mountedNavigator);
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.mountID, mountID);
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.stack, 'Canvas,Thread');
  assert.equal(renderer!.root.findByProps({ testID: 'root-navigation' }).props.threadDraft, 'reply in progress');
  assert.equal(renderer!.root.findAllByProps({ accessibilityRole: 'tablist' }).length, 1, 'sidebar tablist did not mount after resize');
  assert.deepEqual(testGlobal.__nativeShellAnnouncements, ['Work selected'], 'resize or owner restore announced a false destination');
  await act(async () => { renderer!.unmount(); });
});

test('deep destinations preserve their owning top-level context', () => {
  const coordinator = createNativeShellSelectionCoordinator(() => {}, 'work');
  assert.equal(coordinator.commit('Thread'), 'work');
  assert.equal(nativeShellDestinationForRoute('Board'), 'work');
  assert.equal(nativeShellDestinationForRoute('NetworkPreview'), 'network');
  assert.equal(nativeShellDestinationForRoute('ContactInbox'), 'work-search');
  assert.equal(nativeShellDestinationForRoute('WorkRecord'), 'you');
});

test('focused Scout, meeting, auth, and web flows remain full-screen', () => {
  for (const route of ['Login', 'Thread', 'Room', 'CreateRoom', 'OSWeb'] as const) {
    assert.equal(nativeShellVisibleForRoute(route), false, route);
  }
  assert.equal(nativeShellVisibleForRoute('Canvas'), true);
  assert.equal(nativeShellVisibleForRoute('NetworkHome'), true);
});

test('compact and iPad compositions are accessible, touch-safe, and resize-driven', () => {
  const shell = source('src', 'navigation', 'NativeUniversalShell.tsx');
  assert.match(shell, /useWindowDimensions\(\)/);
  assert.match(shell, /nativeShellLayout\(width\)/);
  assert.match(shell, /accessibilityRole="tablist"/);
  assert.match(shell, /accessibilityRole="tab"/);
  assert.match(shell, /accessibilityState=\{\{ selected \}\}/);
  assert.match(shell, /minHeight: 48/);
  assert.match(shell, /minHeight: 52/);
  assert.match(shell, /transform: \[\{ scale: 0\.96 \}\]/);
  assert.match(shell, /maxFontSizeMultiplier=\{2\}/);
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
  assert.match(root, /navigationRef\.navigate\(route as never\)/);
  assert.doesNotMatch(root, /navigationRef\.resetRoot/);
  assert.match(root, /createNativeShellSelectionCoordinator/);
  assert.match(root, /shellSelectionRef\.current\.commit\(route\)/);
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
  assert.match(screens, /keyboardDismissMode="on-drag"/);
  assert.match(screens, /keyboardShouldPersistTaps="handled"/);
  assert.match(screens, /No public child surface or fixture data is mounted/);
  assert.doesNotMatch(screens, /Northstar|Bonfire|Alex|fictional_/);
});
