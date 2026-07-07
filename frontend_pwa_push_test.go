package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Card 089 — the installable-PWA + phone-push frontend contract, in the
// index.html grep-pin grammar (frontend_manifest_test.go style).

func TestIndexPWAPushWiring(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)

	for _, want := range []string{
		// iOS home-screen tile needs a PNG apple-touch-icon (SVG is ignored).
		`<link rel="apple-touch-icon" href="/public/apple-touch-icon.png">`,
		// iOS push requires launching standalone from a home-screen install.
		`name="apple-mobile-web-app-capable" content="yes"`,
		// Service worker registered from the root so its scope is "/".
		`navigator.serviceWorker.register('/sw.js', { updateViaCache: 'none' })`,
		// The settings surface for push.
		`data-settings-section="notifications"`,
		// The VAPID applicationServerKey conversion helper.
		"function urlBase64ToUint8Array(base64String)",
		"applicationServerKey: urlBase64ToUint8Array(pushVapidPublicKey)",
		// The four push endpoints the client drives.
		"/assistant/push/config",
		"/assistant/push/subscribe",
		"/assistant/push/unsubscribe",
		"/assistant/push/prefs",
		// userVisibleOnly is mandatory for a Web Push subscription.
		"userVisibleOnly: true",
		// The bell deep-link param + its one-shot consumer.
		"get('bell')",
		"function routeBellDeepLink()",
		"openNotificationEntry(entry)",
		// iOS caveat copy — there is no install-prompt API on iOS.
		"Add to Home Screen",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing PWA/push hook %q", want)
		}
	}

	// The deep-link routing runs after notifications load (so the entry exists).
	if !strings.Contains(html, "loadNotifications().then(() => routeBellDeepLink())") &&
		!strings.Contains(html, "loadNotifications().then(() => { if (typeof routeBellDeepLink === 'function') routeBellDeepLink() })") {
		t.Fatal("routeBellDeepLink must run after loadNotifications resolves")
	}
}

// The service worker on disk shows notifications, routes taps, and is
// deliberately cache-free (index.html is served no-store; a caching SW would
// pin a stale shell).
func TestServiceWorkerFileContract(t *testing.T) {
	raw, err := os.ReadFile("public/sw.js")
	if err != nil {
		t.Fatalf("read public/sw.js: %v", err)
	}
	sw := string(raw)

	for _, want := range []string{
		"addEventListener('install'",
		"skipWaiting()",
		"addEventListener('activate'",
		"clients.claim()",
		"addEventListener('push'",
		"showNotification(",
		"addEventListener('notificationclick'",
		"openWindow(",
		// routes a tap into an already-open client
		"postMessage({ type: 'bell', id: bellId })",
	} {
		if !strings.Contains(sw, want) {
			t.Fatalf("public/sw.js missing %q", want)
		}
	}

	// Cache-free: no fetch handler, no Cache Storage use.
	for _, banned := range []string{"addEventListener('fetch'", "caches.open", "caches.match"} {
		if strings.Contains(sw, banned) {
			t.Fatalf("public/sw.js must stay cache-free but contains %q", banned)
		}
	}
}

// The manifest ships raster icons (Android installability + iOS) and installs
// standalone.
func TestManifestHasPNGIconsAndStandalone(t *testing.T) {
	raw, err := os.ReadFile("public/manifest.webmanifest")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest struct {
		Display string `json:"display"`
		Icons   []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Type    string `json:"type"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if manifest.Display != "standalone" {
		t.Fatalf("manifest display=%q, want standalone", manifest.Display)
	}

	var has192, has512, hasMaskable bool
	for _, icon := range manifest.Icons {
		if icon.Type != "image/png" {
			continue
		}
		if icon.Sizes == "192x192" {
			has192 = true
		}
		if icon.Sizes == "512x512" {
			has512 = true
			if strings.Contains(icon.Purpose, "maskable") {
				hasMaskable = true
			}
		}
	}
	if !has192 || !has512 {
		t.Fatalf("manifest icons=%#v, want 192x192 and 512x512 PNGs", manifest.Icons)
	}
	if !hasMaskable {
		t.Fatal("manifest must include a maskable 512 PNG for Android adaptive icons")
	}

	// The referenced icon files must exist on disk (Dockerfile copies public/).
	for _, path := range []string{
		"public/icon-192.png",
		"public/icon-512.png",
		"public/icon-maskable-512.png",
		"public/apple-touch-icon.png",
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing icon asset %s: %v", path, err)
		}
	}
}
