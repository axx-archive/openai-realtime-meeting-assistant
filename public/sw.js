/* Bonfire service worker (card 089).
 *
 * Deliberately CACHE-FREE. index.html is served no-store by design; a caching
 * service worker would pin users to a stale app shell that only a manual
 * unregister could clear. This worker exists solely to (a) satisfy the
 * home-screen-install + Web Push requirement and (b) route push taps. It never
 * intercepts fetches. */

self.addEventListener('install', () => {
  // Take over immediately so a redeployed worker controls the page on next load
  // instead of waiting for every tab to close.
  self.skipWaiting()
})

self.addEventListener('activate', event => {
  event.waitUntil(self.clients.claim())
})

self.addEventListener('push', event => {
  // Payload is JSON: { title, body, tag, url }. A malformed/empty payload still
  // shows *something* — userVisibleOnly means every push must display a
  // notification or the push service throttles future ones.
  let data = {}
  if (event.data) {
    try {
      data = event.data.json()
    } catch (err) {
      data = { body: event.data.text() }
    }
  }
  const title = data.title || 'Bonfire'
  const url = data.url || '/'
  const options = {
    body: data.body || '',
    tag: data.tag || undefined,
    icon: '/public/icon-192.png',
    badge: '/public/icon-192.png',
    data: { url: url, id: data.tag || '' }
  }
  event.waitUntil(self.registration.showNotification(title, options))
})

self.addEventListener('notificationclick', event => {
  event.notification.close()
  const target = (event.notification.data && event.notification.data.url) || '/'
  const bellId = (event.notification.data && event.notification.data.id) || ''
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(clients => {
      for (const client of clients) {
        // A window is already open: focus it and let the app route in place
        // (same bell-deep-link path the ?bell= URL param drives on cold boot).
        if ('focus' in client) {
          client.focus()
          if (bellId) {
            client.postMessage({ type: 'bell', id: bellId })
          }
          return undefined
        }
      }
      // No open window: open one at the deep link so the app routes on boot.
      if (self.clients.openWindow) {
        return self.clients.openWindow(target)
      }
      return undefined
    })
  )
})
