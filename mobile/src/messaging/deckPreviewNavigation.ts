export type DeckPreviewNavigationState = {
  status: 'loading' | 'ready' | 'error';
  currentIndex: number;
  slideCount: number;
};

const DECK_PREVIEW_MESSAGE_KIND = 'bonfire:deck-preview-navigation';
const MAX_PREVIEW_SLIDES = 500;

export function initialDeckPreviewNavigationState(): DeckPreviewNavigationState {
  return { status: 'loading', currentIndex: 0, slideCount: 0 };
}

export function parseDeckPreviewNavigationMessage(value: unknown): DeckPreviewNavigationState | null {
  let payload: unknown = value;
  if (typeof value === 'string') {
    try {
      payload = JSON.parse(value);
    } catch {
      return null;
    }
  }
  if (!payload || typeof payload !== 'object') return null;
  const message = payload as Record<string, unknown>;
  if (message.kind !== DECK_PREVIEW_MESSAGE_KIND) return null;
  if (message.status === 'error') {
    return { status: 'error', currentIndex: 0, slideCount: 0 };
  }
  if (message.status !== 'ready') return null;
  const currentIndex = Number(message.currentIndex);
  const slideCount = Number(message.slideCount);
  if (
    !Number.isInteger(currentIndex)
    || !Number.isInteger(slideCount)
    || slideCount < 1
    || slideCount > MAX_PREVIEW_SLIDES
    || currentIndex < 0
    || currentIndex >= slideCount
  ) {
    return null;
  }
  return { status: 'ready', currentIndex, slideCount };
}

export function deckPreviewNavigationTarget(
  state: DeckPreviewNavigationState,
  direction: 'previous' | 'next',
): number | null {
  if (state.status !== 'ready' || state.slideCount < 1) return null;
  const target = Math.max(
    0,
    Math.min(state.slideCount - 1, state.currentIndex + (direction === 'next' ? 1 : -1)),
  );
  return target === state.currentIndex ? null : target;
}

export function deckPreviewNavigationCommand(index: number): string {
  const safeIndex = Number.isInteger(index) ? Math.max(0, Math.min(MAX_PREVIEW_SLIDES - 1, index)) : 0;
  return `(function(){var controller=window.__bonfireDeckPreview;if(controller&&typeof controller.show==='function'){controller.show(${safeIndex});}true;})();`;
}

/**
 * The native host owns preview navigation so every deck uses the same controls.
 * The injected controller only discovers top-level slide pages, never nested
 * slide content, and reports its authoritative count/index back to React Native.
 */
export const DECK_PREVIEW_NAVIGATION_JS = `
(function() {
  'use strict';
  var MESSAGE_KIND = '${DECK_PREVIEW_MESSAGE_KIND}';
  var stage = document.getElementById('stage');
  var pages = [];
  if (stage) {
    pages = Array.prototype.filter.call(stage.children, function(child) {
      return child.matches('[data-deck-slide], [data-slide-id], .pg, .slide, section');
    });
  }
  if (!pages.length) {
    pages = Array.prototype.slice.call(document.querySelectorAll('body > [data-deck-slide], body > [data-slide-id], body > .pg, body > .slide, body > section'));
  }
  var originals = pages.map(function(page) {
    return {
      display: page.style.getPropertyValue('display'),
      priority: page.style.getPropertyPriority('display')
    };
  });
  var index = 0;
  function post(status) {
    if (!window.ReactNativeWebView || typeof window.ReactNativeWebView.postMessage !== 'function') return;
    window.ReactNativeWebView.postMessage(JSON.stringify({
      kind: MESSAGE_KIND,
      status: status,
      currentIndex: index,
      slideCount: pages.length
    }));
  }
  function restoreDisplay(page, original) {
    if (original.display) {
      page.style.setProperty('display', original.display, original.priority || '');
    } else {
      page.style.removeProperty('display');
    }
  }
  function show(next) {
    if (!pages.length) {
      post('error');
      return;
    }
    index = Math.max(0, Math.min(pages.length - 1, Number(next) || 0));
    pages.forEach(function(page, pageIndex) {
      var active = pageIndex === index;
      if (active) {
        restoreDisplay(page, originals[pageIndex]);
        page.classList.add('on');
        if (window.getComputedStyle(page).display === 'none') {
          page.style.setProperty('display', 'block', 'important');
        }
      } else {
        page.classList.remove('on');
        page.style.setProperty('display', 'none', 'important');
      }
      page.setAttribute('aria-hidden', active ? 'false' : 'true');
    });
    post('ready');
  }
  var embeddedNavigation = document.querySelector('#deck-viewer-nav[data-deck-navigation="trusted"]');
  if (embeddedNavigation) embeddedNavigation.style.setProperty('display', 'none', 'important');
  document.body.style.overflow = 'hidden';
  window.__bonfireDeckPreview = Object.freeze({ show: show });
  if (!pages.length) {
    post('error');
    return;
  }
  var authoredIndex = pages.findIndex(function(page) { return page.classList.contains('on'); });
  show(authoredIndex >= 0 ? authoredIndex : 0);
})();
true;
`;
