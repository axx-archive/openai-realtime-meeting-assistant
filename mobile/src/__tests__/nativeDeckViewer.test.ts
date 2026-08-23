import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';

import {
  nativeDeckFrame,
  nativeDeckPreviewPath,
  nativeDeckRenderPath,
  nativeTextArtifactIsRenderable,
} from '../artifacts/nativeDeckViewer';
import { registerTestStubModules } from './support/registerTestStubModules';

test('native deck viewer preserves 16:9 on phone, iPad, rotation, and split view', () => {
  for (const [width, height] of [[390, 844], [844, 390], [1024, 1366], [744, 1024], [1366, 1024]]) {
    const frame = nativeDeckFrame(width, height);
    assert.ok(frame.width <= width);
    assert.ok(frame.height <= height);
    assert.ok(Math.abs(frame.width / frame.height - (16 / 9)) < 0.01);
  }
  assert.equal(nativeDeckFrame(390, 844).compact, true);
  assert.equal(nativeDeckFrame(1024, 1366).compact, false);
});

test('native deck viewer admits only the signed render route and never Studio JSON', () => {
  assert.equal(nativeDeckRenderPath('/artifacts/render?id=deck&token=signed'), '/artifacts/render?id=deck&token=signed');
  assert.equal(nativeDeckRenderPath('/artifacts?id=deck'), null);
  assert.equal(nativeDeckRenderPath('/studio/deck/deck?mode=edit'), null);
  assert.equal(nativeDeckRenderPath('//attacker.example/artifacts/render?token=x'), null);
  assert.equal(
    nativeDeckPreviewPath('deck one', 7, 'A'.repeat(64)),
    `/artifacts/preview?id=deck%20one&version=7&digest=${'a'.repeat(64)}`,
  );
  assert.equal(nativeDeckPreviewPath('', 7, 'a'.repeat(64)), null);
  assert.equal(nativeDeckPreviewPath('deck', 0, 'a'.repeat(64)), null);
  assert.equal(nativeDeckPreviewPath('deck', 7, 'not-a-digest'), null);
  assert.equal(nativeTextArtifactIsRenderable('A concise research finding.'), true);
  assert.equal(nativeTextArtifactIsRenderable('[Finding](https://example.test)'), true);
  assert.equal(nativeTextArtifactIsRenderable('{A human-written aside}'), true);
  assert.equal(nativeTextArtifactIsRenderable('{"artifact":{"slides":[]}}'), false);
  assert.equal(nativeTextArtifactIsRenderable('[{"type":"slide"}]'), false);
  assert.equal(nativeTextArtifactIsRenderable('"serialized deck state"'), false);
  assert.equal(nativeTextArtifactIsRenderable('null'), false);
  assert.equal(nativeTextArtifactIsRenderable('42'), false);
  assert.equal(nativeTextArtifactIsRenderable('<html><body>deck</body></html>'), false);

  const root = path.resolve(import.meta.dirname, '..');
  const viewer = readFileSync(path.join(root, 'screens', 'DeckViewerScreen.tsx'), 'utf8');
  const thread = readFileSync(path.join(root, 'screens', 'ThreadScreen.tsx'), 'utf8');
  const files = readFileSync(path.join(root, 'screens', 'FilesScreen.tsx'), 'utf8');
  const inline = readFileSync(path.join(root, 'messaging', 'InlineArtifactPreview.tsx'), 'utf8');
  assert.match(viewer, /api\.artifactRenderToken/u);
  assert.doesNotMatch(viewer, /api\.artifact\(/u);
  assert.doesNotMatch(viewer, /artifactStudioPath|mode=edit/u);
  assert.match(viewer, /Editing is available on desktop/u);
  assert.match(viewer, /route\.params\.desktopEditable === true/u);
  assert.match(viewer, /injectedJavaScript=\{DECK_PREVIEW_NAVIGATION_JS\}/u);
  assert.match(viewer, /!ready && styles\.webViewHidden/u);
  assert.match(inline, /injectedJavaScript=\{DECK_PREVIEW_NAVIGATION_JS\}/u);
  assert.match(inline, /nativeDeckPreviewPath/u);
  assert.doesNotMatch(inline, /api\.artifactRenderToken/u);
  assert.match(inline, /headers: buildAuthHeaders\(NATIVE_CLIENT_HEADER, sessionToken/u);
  assert.match(inline, /deckNavigation\.status !== 'ready' && styles\.deckWebViewHidden/u);
  assert.match(thread, /navigation\.navigate\('DeckViewer'/u);
  assert.match(thread, /nativeTextArtifactIsRenderable\(text\)/u);
  assert.match(files, /studioKind === 'deck'[\s\S]*navigation\.navigate\('DeckViewer'/u);
  assert.match(files, /if \(!nativeTextArtifactIsRenderable\(text\)\)[\s\S]*structured deliverable needs its supported viewer/u);
});

test('full-screen viewer renders desktop editing only for exact writable capability', async () => {
  registerTestStubModules('native-deck-viewer-stub:', {
    'native-deck-viewer-stub:react-native': `
      export const ActivityIndicator='ActivityIndicator'; export const Pressable='Pressable'; export const Text='Text'; export const View='View';
      export const Linking={openURL:async()=>{}}; export const useWindowDimensions=()=>({width:390,height:844});
      export const StyleSheet={create:value=>value,hairlineWidth:1};
    `,
    'native-deck-viewer-stub:react-native-safe-area-context': `
      export const SafeAreaView='SafeAreaView'; export const useSafeAreaInsets=()=>({top:0,right:0,bottom:0,left:0});
    `,
    'native-deck-viewer-stub:expo-symbols': `export const SymbolView='SymbolView';`,
    'native-deck-viewer-stub:react-native-webview': `
      const React=globalThis.__nativeDeckViewerReact;
      export const WebView=React.forwardRef(function WebView(props, ref) {
        React.useImperativeHandle(ref, ()=>({injectJavaScript:()=>{}}));
        return React.createElement('WebViewHost', props);
      });
    `,
    'native-deck-viewer-stub:../api/client': `export const api={artifactRenderToken:async()=>({url:'/artifacts/render?id=deck&token=signed'})};`,
    'native-deck-viewer-stub:../api/requestHelpers': `export const buildApiUrl=(base,path)=>base+path;`,
    'native-deck-viewer-stub:../auth/AuthContext': `export const useAuth=()=>({sessionToken:'session'});`,
    'native-deck-viewer-stub:../config': `export const API_BASE_URL='https://example.test';`,
    'native-deck-viewer-stub:../theme/glass': `export const Glass='Glass';`,
    'native-deck-viewer-stub:../theme/tokens': `
      const proxy=new Proxy({}, {get:()=>0}); export const colors=proxy; export const radius=proxy; export const space=proxy; export const type=proxy; export const hitMin=44;
    `,
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const React = (await import('react')).default;
  (globalThis as typeof globalThis & { __nativeDeckViewerReact?: typeof React }).__nativeDeckViewerReact = React;
  const { act, create } = await import('react-test-renderer');
  const { DeckViewerScreen } = await import('../screens/DeckViewerScreen');
  const navigation = { goBack: () => undefined };
  const route = (desktopEditable: boolean) => ({
    key: 'deck-viewer',
    name: 'DeckViewer',
    params: { artifactId: 'deck', title: 'Field Network', desktopEditable },
  });
  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => {
    renderer = create(React.createElement(DeckViewerScreen as React.ComponentType<any>, {
      navigation,
      route: route(false),
    }));
    await Promise.resolve();
  });
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Editing is available on desktop' }).length, 0);

  await act(async () => {
    renderer!.update(React.createElement(DeckViewerScreen as React.ComponentType<any>, {
      navigation,
      route: route(true),
    }));
  });
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Editing is available on desktop' }).length, 1);
  await act(async () => { renderer!.unmount(); });
});
