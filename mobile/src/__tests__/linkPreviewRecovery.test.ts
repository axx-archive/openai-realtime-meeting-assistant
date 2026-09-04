import assert from 'node:assert/strict';
import test from 'node:test';
import type { LinkPreview } from '../api/types';
import { registerTestStubModules } from './support/registerTestStubModules';

test('native rich media recovers from missing metadata and failed posters without dead links', async () => {
  const loaded: string[] = [];
  let response: LinkPreview | null = null;
  const opened: string[] = [];
  const alerts: unknown[] = [];
  let failOpening = false;
  (globalThis as any).__linkPreview = async (_session: string, url: string) => { loaded.push(url); return { preview: response }; };
  (globalThis as any).__linkOpen = async (url: string) => { opened.push(url); if (failOpening) throw new Error('No handler'); };
  (globalThis as any).__linkAlert = (...args: unknown[]) => alerts.push(args);
  registerTestStubModules('link-recovery:', {
    'link-recovery:react-native': `export const ActivityIndicator='ActivityIndicator'; export const Pressable='Pressable'; export const Text='Text'; export const View='View'; export const Alert={alert:(...args)=>globalThis.__linkAlert(...args)}; export const StyleSheet={create:v=>v,hairlineWidth:1,absoluteFill:{}};`,
    'link-recovery:expo-image': `export const Image='Image';`,
    'link-recovery:expo-symbols': `export const SymbolView='SymbolView';`,
    'link-recovery:expo-linking': `export const openURL=url=>globalThis.__linkOpen(url);`,
    'link-recovery:@shopify/flash-list': `import React from 'react'; export function useRecyclingState(initial,deps){const [value,setValue]=React.useState(initial); React.useEffect(()=>setValue(initial),deps); return [value,setValue];}`,
    'link-recovery:../api/client': `export const api={linkPreview:(session,url)=>globalThis.__linkPreview(session,url)};`,
    'link-recovery:../config': `export const API_BASE_URL='https://example.test'; export const NATIVE_CLIENT_HEADER='expo';`,
    'link-recovery:../theme/tokens': `const proxy=new Proxy({}, {get:()=>0}); export const colors=proxy; export const radius=proxy; export const space=proxy; export const type=proxy;`,
    'link-recovery:./linkPreviewCache': `export const cachedLinkPreview=(url,loader)=>loader();`,
  });
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  const React = await import('react');
  const { act, create } = await import('react-test-renderer');
  const { LinkPreviewCard } = await import('../messaging/LinkPreviewCard');
  const props = { url: 'https://youtu.be/abcdefghijk', sessionToken: 'viewer', own: false, seamless: true };
  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => { renderer = create(React.createElement(LinkPreviewCard, props)); });
  assert.equal(loaded.length, 1);
  assert.match(JSON.stringify(renderer!.toJSON()), /youtu.be/u);
  await act(async () => { renderer!.root.findByType('Pressable' as any).props.onPress(); });
  assert.deepEqual(opened, [props.url]);
  await act(async () => { renderer!.unmount(); });
  response = { url: 'https://www.youtube.com/watch?v=abcdefghijk', kind: 'youtube_video', title: 'A working preview', imageUrl: '/assistant/link-preview/image?url=poster', siteName: 'YouTube' };
  await act(async () => { renderer = create(React.createElement(LinkPreviewCard, props)); });
  assert.equal(loaded.length, 2, 'a prior null must not prevent retry when the card is mounted again');
  const poster = renderer!.root.findByType('Image' as any);
  assert.equal(poster.props.source.uri, 'https://example.test/assistant/link-preview/image?url=poster');
  await act(async () => { poster.props.onError(); });
  assert.equal(renderer!.root.findAllByType('Image' as any).length, 0);
  assert.match(JSON.stringify(renderer!.toJSON()), /YouTube · video/u);
  failOpening = true;
  await act(async () => { renderer!.root.findByType('Pressable' as any).props.onPress(); });
  assert.equal(opened[1], response.url);
  assert.equal(alerts.length, 1, 'failed OS link opening must be visible');
  await act(async () => { renderer!.unmount(); });
  response = { url: 'https://x.com/i/article/1234567890', kind: 'article', title: 'Long-form article', imageUrl: '/assistant/link-preview/image?url=cover', siteName: 'X', mediaType: 'article' };
  await act(async () => { renderer = create(React.createElement(LinkPreviewCard, { ...props, url: response!.url })); });
  assert.match(JSON.stringify(renderer!.toJSON()), /X · article/u);
  assert.equal(renderer!.root.findByType('Image' as any).props.contentFit, 'cover');
  assert.equal(renderer!.root.findAllByProps({ name: 'play.fill' }).length, 0);
  const style = renderer!.root.findByType('Pressable' as any).props.style({ pressed: false }).filter(Boolean)[0];
  assert.equal(style.minWidth, 0);
  assert.equal(style.maxWidth, '100%');
  await act(async () => { renderer!.unmount(); });
  response = { url: 'https://x.com/example/status/123', kind: 'x_post', title: 'Author', authorName: 'Author', description: 'A post with a photo', imageUrl: '/assistant/link-preview/image?url=photo' };
  await act(async () => { renderer = create(React.createElement(LinkPreviewCard, { ...props, url: response!.url })); });
  assert.equal(renderer!.root.findByType('Image' as any).props.accessibilityLabel, 'Post image', 'legacy unspecified X images must not become author avatars');
  await act(async () => { renderer!.root.findByType('Image' as any).props.onError(); });
  assert.equal(renderer!.root.findAllByType('Image' as any).length, 0);
  assert.match(JSON.stringify(renderer!.toJSON()), /A post with a photo/u);
  await act(async () => { renderer!.unmount(); });
  response = { ...response, imageRole: 'author_avatar' };
  await act(async () => { renderer = create(React.createElement(LinkPreviewCard, { ...props, url: response!.url })); });
  assert.equal(renderer!.root.findByType('Image' as any).props.style.width, 38);
  await act(async () => { renderer!.unmount(); });
  delete (globalThis as any).__linkPreview; delete (globalThis as any).__linkOpen; delete (globalThis as any).__linkAlert;
});
