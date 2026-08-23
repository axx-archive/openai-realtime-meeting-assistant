import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath, URL } from 'node:url';
import test from 'node:test';

function source(path: string): string {
  return readFileSync(fileURLToPath(new URL(`../../${path}`, import.meta.url)), 'utf8');
}

test('work status supports intentional horizontal swipe plus a 44-point accessible dismissal', () => {
  const pill = source('src/messaging/WorkActivityPill.tsx');
  assert.match(pill, /PanResponder\.create/u);
  assert.match(pill, /Math\.abs\(gesture\.dx\) > Math\.abs\(gesture\.dy\) \* swipeHorizontalBias/u);
  assert.match(pill, /swipeDismissDistance = 72/u);
  assert.match(pill, /useNativeDriver: true/u);
  assert.match(pill, /accessibilityLabel="Dismiss work status"/u);
  assert.match(pill, /width: 44,[\s\S]*height: 44/u);
  assert.match(pill, /transform: \[\{ scale: 0\.96 \}\]/u);
});

test('dismissal is viewer-local, persisted, and does not mutate shared work', () => {
  const store = source('src/messaging/workActivityDismissalStore.ts');
  const screen = source('src/screens/ThreadScreen.tsx');
  assert.match(store, /SecureStore\.getItemAsync/u);
  assert.match(store, /SecureStore\.setItemAsync/u);
  assert.match(store, /workActivityDismissalStorageKey\(viewerEmail\)/u);
  assert.doesNotMatch(store, /api\.|fetch\(|sendScout|updateScout/u);
  assert.match(screen, /viewerDismissedWorkActivity\(email/u);
  assert.match(screen, /dismissWorkActivityForViewer\(email/u);
  assert.match(screen, /key=\{currentWorkSurfaceToken\}/u);
});

test('governed final work renders as a rich deliverable instead of a generic work card', () => {
  const bubble = source('src/messaging/MessageBubble.tsx');
  const preview = source('src/messaging/InlineArtifactPreview.tsx');
  assert.match(bubble, /governedRichResult && workThread/u);
  assert.match(bubble, /<InlineArtifactPreview[\s\S]*kind="deliverable"/u);
  assert.match(preview, /deliverable: 'Deliverable'/u);
});

