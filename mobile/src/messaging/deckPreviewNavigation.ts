export type DeckPreviewNavigationState = {
  status: 'loading' | 'ready' | 'error';
  currentIndex: number;
  slideCount: number;
};

const DECK_PREVIEW_MESSAGE_KIND = 'bonfire:deck-preview-navigation';
const MAX_PREVIEW_SLIDES = 500;
const DECK_SOURCE_WIDTH = 1920;
const DECK_SOURCE_HEIGHT = 1080;

export type DeckPreviewFitFrame = {
  scale: number;
  left: number;
  top: number;
  width: number;
  height: number;
};

/** The same contain-fit law used by the injected native deck controller. */
export function deckPreviewFitFrame(
  viewportWidth: number,
  viewportHeight: number,
  sourceWidth = DECK_SOURCE_WIDTH,
  sourceHeight = DECK_SOURCE_HEIGHT,
): DeckPreviewFitFrame {
  const safeViewportWidth = Number.isFinite(viewportWidth) ? Math.max(1, viewportWidth) : 1;
  const safeViewportHeight = Number.isFinite(viewportHeight) ? Math.max(1, viewportHeight) : 1;
  const safeSourceWidth = Number.isFinite(sourceWidth) ? Math.max(1, sourceWidth) : DECK_SOURCE_WIDTH;
  const safeSourceHeight = Number.isFinite(sourceHeight) ? Math.max(1, sourceHeight) : DECK_SOURCE_HEIGHT;
  const scale = Math.min(safeViewportWidth / safeSourceWidth, safeViewportHeight / safeSourceHeight);
  const width = safeSourceWidth * scale;
  const height = safeSourceHeight * scale;
  return {
    scale,
    left: (safeViewportWidth - width) / 2,
    top: (safeViewportHeight - height) / 2,
    width,
    height,
  };
}

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
 * The native host owns fit, reveal, and navigation for both direct HTML and
 * signed render URLs. The stage stays hidden until the real 1920x1080 chassis
 * has been contained in the current WebView viewport. The controller discovers
 * only top-level slide pages, never nested slide content, and reports readiness
 * after the first fit so React Native cannot reveal an oversized first frame.
 */
export const DECK_PREVIEW_NAVIGATION_JS = `
(function() {
  'use strict';
  var MESSAGE_KIND = '${DECK_PREVIEW_MESSAGE_KIND}';
  var DEFAULT_WIDTH = ${DECK_SOURCE_WIDTH};
  var DEFAULT_HEIGHT = ${DECK_SOURCE_HEIGHT};
  var root = document.documentElement;
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
  var fitted = false;
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
    if (fitted) post('ready');
  }
  function positiveDimension(value, fallback) {
    var number = Number.parseFloat(String(value || ''));
    return Number.isFinite(number) && number > 0 ? number : fallback;
  }
  function sourceDimension(attribute, property, fallback) {
    var declared = stage && typeof stage.getAttribute === 'function' ? stage.getAttribute(attribute) : '';
    if (declared) return positiveDimension(declared, fallback);
    var inline = stage ? stage.style.getPropertyValue(property) : '';
    if (inline) return positiveDimension(inline, fallback);
    var computed = stage ? window.getComputedStyle(stage)[property] : '';
    return positiveDimension(computed, fallback);
  }
  function fit() {
    if (!stage) {
      post('error');
      return;
    }
    var viewportWidth = Math.max(1, Number(window.innerWidth) || Number(root.clientWidth) || 1);
    var viewportHeight = Math.max(1, Number(window.innerHeight) || Number(root.clientHeight) || 1);
    var sourceWidth = sourceDimension('data-deck-width', 'width', DEFAULT_WIDTH);
    var sourceHeight = sourceDimension('data-deck-height', 'height', DEFAULT_HEIGHT);
    var scale = Math.min(viewportWidth / sourceWidth, viewportHeight / sourceHeight);
    var fittedWidth = sourceWidth * scale;
    var fittedHeight = sourceHeight * scale;
    stage.style.setProperty('position', 'absolute', 'important');
    stage.style.setProperty('width', sourceWidth + 'px', 'important');
    stage.style.setProperty('height', sourceHeight + 'px', 'important');
    stage.style.setProperty('transform-origin', 'top left', 'important');
    stage.style.setProperty('transform', 'scale(' + scale + ')', 'important');
    stage.style.setProperty('left', ((viewportWidth - fittedWidth) / 2) + 'px', 'important');
    stage.style.setProperty('top', ((viewportHeight - fittedHeight) / 2) + 'px', 'important');
    stage.style.setProperty('visibility', 'visible', 'important');
    root.dataset.bonfireNativeDeckReady = 'true';
    fitted = true;
    post('ready');
  }
  var embeddedNavigation = document.querySelector('#deck-viewer-nav[data-deck-navigation="trusted"]');
  if (embeddedNavigation) embeddedNavigation.style.setProperty('display', 'none', 'important');
  document.body.style.setProperty('margin', '0', 'important');
  document.body.style.setProperty('overflow', 'hidden', 'important');
  root.style.setProperty('overflow', 'hidden', 'important');
  if (!stage || !pages.length) {
    post('error');
    return;
  }
  stage.style.setProperty('visibility', 'hidden', 'important');
  window.__bonfireDeckPreview = Object.freeze({ show: show, fit: fit });
  window.addEventListener('resize', fit);
  var authoredIndex = pages.findIndex(function(page) { return page.classList.contains('on'); });
  show(authoredIndex >= 0 ? authoredIndex : 0);
  fit();
})();
true;
`;
