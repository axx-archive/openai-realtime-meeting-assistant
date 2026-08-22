import assert from 'node:assert/strict';
import test from 'node:test';
import { registerTestStubModules } from './support/registerTestStubModules';

type NativeShareCall = { sessionToken: string; request: { kind: string; format: string; artifactId: string } };

test('OSWeb routes iPhone and iPad Studio downloads through native authenticated sharing', async () => {
  registerTestStubModules('studio-download-screen-stub:', {
    'studio-download-screen-stub:react-native': `
      export const ActivityIndicator='ActivityIndicator'; export const Pressable='Pressable';
      export const Text='Text'; export const View='View';
      export const Linking={openURL:async url=>{globalThis.__studioExternalLinks.push(url)}};
      export const StyleSheet={hairlineWidth:1,absoluteFill:{},create:value=>value};
    `,
    'studio-download-screen-stub:react-native-webview': `export const WebView='WebView';`,
    'studio-download-screen-stub:react-native-safe-area-context': `export const SafeAreaView='SafeAreaView';`,
    'studio-download-screen-stub:../auth/AuthContext': `
      export function useAuth(){return {sessionToken:'native-session',user:{name:'AJ'}};}
    `,
    'studio-download-screen-stub:../config': `
      export const WEB_APP_URL='https://thebonfire.xyz';
    `,
    'studio-download-screen-stub:../theme/tokens': `
      const colors=new Proxy({}, {get:()=> '#111'}); const type=new Proxy({}, {get:()=> ({})});
      export {colors,type}; export const product={name:'Stride'};
    `,
    'studio-download-screen-stub:../artifacts/studioDownloads': `
      export async function shareStudioDownload(sessionToken,request){
        globalThis.__studioNativeShares.push({sessionToken,request});
      }
    `,
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const nativeShares: NativeShareCall[] = [];
  (globalThis as typeof globalThis & { __studioNativeShares: NativeShareCall[] }).__studioNativeShares = nativeShares;
  (globalThis as typeof globalThis & { __studioExternalLinks: string[] }).__studioExternalLinks = [];
  const React = (await import('react')).default;
  const { act, create } = await import('react-test-renderer');
  const { OSWebScreen } = await import('../screens/OSWebScreen');
  const ref = 'a'.repeat(64);

  for (const fixture of [
    { device: 'iPhone', kind: 'deck', artifactId: 'deck-42', mode: 'edit' },
    { device: 'iPad', kind: 'document', artifactId: 'doc-42', mode: 'view' },
  ]) {
    const fileName = `${fixture.device} review.pdf`;
    const route = {
      key: fixture.device,
      name: 'OSWeb' as const,
      params: { path: `/studio/${fixture.kind}/${fixture.artifactId}?mode=${fixture.mode}`, title: fixture.device },
    };
    let renderer: import('react-test-renderer').ReactTestRenderer;
    await act(async () => {
      renderer = create(React.createElement(OSWebScreen, {
        route: route as never,
        navigation: { goBack() {} } as never,
      }));
    });
    const webView = renderer!.root.findByType('WebView' as never);
    await act(async () => {
      webView.props.onMessage({
        nativeEvent: {
          data: JSON.stringify({
            type: 'stride.studio.download', version: 1, kind: fixture.kind, format: 'pdf',
            artifactId: fixture.artifactId, fileName,
            url: `/artifacts/blob?ref=${ref}&name=${encodeURIComponent(fileName)}`,
          }),
        },
      });
    });
    await act(async () => renderer!.unmount());
  }

  assert.deepEqual(nativeShares.map(({ sessionToken, request }) => ({ sessionToken, ...request })), [
    { sessionToken: 'native-session', kind: 'deck', format: 'pdf', artifactId: 'deck-42', fileName: 'iPhone review.pdf', downloadUrl: `https://thebonfire.xyz/artifacts/blob?ref=${ref}&name=iPhone%20review.pdf` },
    { sessionToken: 'native-session', kind: 'document', format: 'pdf', artifactId: 'doc-42', fileName: 'iPad review.pdf', downloadUrl: `https://thebonfire.xyz/artifacts/blob?ref=${ref}&name=iPad%20review.pdf` },
  ]);
});

test('OSWeb surfaces an explicit error when WKWebView offers an unsafe download', async () => {
  const React = (await import('react')).default;
  const { act, create } = await import('react-test-renderer');
  const { OSWebScreen } = await import('../screens/OSWebScreen');
  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => {
    renderer = create(React.createElement(OSWebScreen, {
      route: { key: 'unsafe', name: 'OSWeb', params: { path: '/studio/deck/deck-42?mode=edit' } } as never,
      navigation: { goBack() {} } as never,
    }));
  });
  await act(async () => {
    renderer!.root.findByType('WebView' as never).props.onFileDownload({
      nativeEvent: { downloadUrl: 'blob:https://thebonfire.xyz/opaque' },
    });
  });
  let copy = renderer!.root.findAllByType('Text' as never).flatMap((node) => node.children).join(' ');
  assert.match(copy, /Stride blocked an unsafe or unsupported download/);
  await act(async () => {
    renderer!.root.findByType('WebView' as never).props.onMessage({
      nativeEvent: {
        data: JSON.stringify({
          type: 'stride.studio.download', version: 1, kind: 'deck', format: 'pptx',
          artifactId: 'another-deck', fileName: 'Blocked.pptx', expectedVersion: 1, sceneRef: 'b'.repeat(64),
        }),
      },
    });
  });
  copy = renderer!.root.findAllByType('Text' as never).flatMap((node) => node.children).join(' ');
  assert.match(copy, /Stride blocked an invalid Studio download request/);
  await act(async () => renderer!.unmount());
});
