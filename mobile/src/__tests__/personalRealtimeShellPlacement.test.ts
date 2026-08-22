import assert from 'node:assert/strict';
import test from 'node:test';
import { registerTestStubModules } from './support/registerTestStubModules';

function flattenStyle(value: unknown): Record<string, unknown> {
  const styles = Array.isArray(value) ? value.flat(Infinity) : [value];
  return Object.assign({}, ...styles.filter((style) => style && typeof style === 'object'));
}

test('rendered focused Thread owns a voice lane outside header and variable composer controls', async () => {
  const testGlobal = globalThis as typeof globalThis & {
    __voiceShellWidth?: number;
    __voiceShellInsets?: { top: number; right: number; bottom: number; left: number };
  };
  registerTestStubModules('voice-shell-stub:', {
    'voice-shell-stub:react-native': `export const Pressable='Pressable'; export const Text='Text'; export const View='View'; export const StyleSheet={create:value=>value,hairlineWidth:1,absoluteFill:{},absoluteFillObject:{}}; export const Platform={OS:'ios',isPad:true}; export const DynamicColorIOS=value=>({dynamic:value}); export const Easing={bezier:()=>()=>0}; export const AccessibilityInfo={isReduceTransparencyEnabled:async()=>false,isReduceMotionEnabled:async()=>false,addEventListener:()=>({remove(){}})}; export const useWindowDimensions=()=>({width:globalThis.__voiceShellWidth||390,height:844,fontScale:1});`,
    'voice-shell-stub:react-native-svg': `const Svg='Svg'; export default Svg; export const Path='Path'; export const Circle='Circle'; export const Ellipse='Ellipse';`,
    'voice-shell-stub:expo-symbols': `export const SymbolView='SymbolView';`,
    'voice-shell-stub:../components/Waveform': `export const Waveform='Waveform';`,
    'voice-shell-stub:react-native-safe-area-context': `export const useSafeAreaInsets=()=>globalThis.__voiceShellInsets||({top:0,right:0,bottom:0,left:0});`,
    'voice-shell-stub:expo-blur': `export const BlurView='BlurView';`,
    'voice-shell-stub:expo-glass-effect': `export const GlassView='GlassView'; export const isLiquidGlassAvailable=()=>false;`,
  });
  testGlobal.__voiceShellWidth = 390;
  testGlobal.__voiceShellInsets = { top: 47, right: 0, bottom: 34, left: 0 };
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const React = (await import('react')).default;
  const { act, create } = await import('react-test-renderer');
  const { NativeUniversalShell } = await import('../navigation/NativeUniversalShell');
  const { PersonalRealtimeContextProvider } = await import('../realtime/PersonalRealtimeContext');
  const Shell = NativeUniversalShell as React.ComponentType<any>;
  const RealtimeProvider = PersonalRealtimeContextProvider as React.ComponentType<any>;

  const controller = (
    status: 'idle' | 'listening' | 'error',
    enabled = true,
    tearingDown = false,
  ) => ({
    enabled,
    status,
    active: status === 'listening',
    tearingDown,
    turn: null,
    error: status === 'error' ? 'Scout voice lost its secure connection.' : null,
    trace: Array.from({ length: 28 }, () => 0),
    threadId: 'private-thread-build-73',
    start: async () => undefined,
    stop: async () => undefined,
  });
  const render = (
    status: 'idle' | 'listening' | 'error',
    keepSidebarForFocusedRoute = false,
    enabled = true,
    tearingDown = false,
  ) => (
    React.createElement(
      RealtimeProvider,
      { value: controller(status, enabled, tearingDown) as never },
      React.createElement(
        Shell,
        {
          active: 'chat',
          keepSidebarForFocusedRoute,
          onSelect: () => undefined,
          personalRealtimeStartAllowed: true,
          personalRealtimeSurface: 'conversation',
          personalRealtimeVisible: false,
          visible: false,
        },
        React.createElement('ThreadProbe', { testID: 'thread-probe' }),
      ),
    )
  );

  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => { renderer = create(render('listening')); });
  assert.equal(flattenStyle(renderer!.root.findByProps({ testID: 'native-shell-content' }).props.style).paddingTop, 64);
  assert.deepEqual(
    flattenStyle(renderer!.root.findByProps({ testID: 'personal-realtime-island' }).props.style),
    {
      alignItems: 'center',
      left: 0,
      position: 'absolute',
      right: 0,
      top: 47,
      zIndex: 10,
    },
  );

  await act(async () => { renderer!.update(render('error')); });
  assert.equal(flattenStyle(renderer!.root.findByProps({ testID: 'native-shell-content' }).props.style).paddingTop, 168);
  assert.equal(
    renderer!.root.findByProps({ accessibilityRole: 'alert' }).props.children,
    'Scout voice lost its secure connection.',
  );

  testGlobal.__voiceShellWidth = 1024;
  await act(async () => { renderer!.update(render('listening', true)); });
  const iPadContent = flattenStyle(renderer!.root.findByProps({ testID: 'native-shell-content' }).props.style);
  const iPadIsland = flattenStyle(renderer!.root.findByProps({ testID: 'personal-realtime-island' }).props.style);
  assert.equal(iPadContent.marginLeft, 68, 'the iPad rail remains visibly reserved');
  assert.equal(iPadContent.paddingTop, 64);
  assert.equal(iPadIsland.left, undefined);
  assert.equal(iPadIsland.right, 16);
  assert.equal(iPadIsland.top, 47);
  assert.equal(renderer!.root.findAllByProps({ accessibilityRole: 'tablist' }).length, 1);

  await act(async () => { renderer!.update(render('listening', true, false, true)); });
  assert.equal(
    renderer!.root.findAllByProps({ testID: 'personal-realtime-island' }).length,
    1,
    'server revocation must retain the active microphone indicator until teardown settles',
  );
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'End Scout voice' }).length, 1);

  await act(async () => { renderer!.update(render('idle', true, false, false)); });
  assert.equal(
    renderer!.root.findAllByProps({ testID: 'personal-realtime-island' }).length,
    0,
    'the revoked island hides only after exact media teardown has completed',
  );
  await act(async () => { renderer!.unmount(); });
});
