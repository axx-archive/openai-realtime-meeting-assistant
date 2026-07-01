package main

import "net/http"

const iceTestHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>MeetingAssist ICE Test</title>
  <style>
    :root { color-scheme: light dark; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #101418; color: #f4f7f5; }
    main { width: min(680px, calc(100vw - 32px)); }
    h1 { margin: 0 0 8px; font-size: 28px; letter-spacing: 0; }
    p { color: #b8c2bd; line-height: 1.5; }
    dl { display: grid; gap: 10px; margin: 24px 0; }
    div.row { display: flex; justify-content: space-between; gap: 16px; border: 1px solid #32403a; border-radius: 8px; padding: 14px 16px; background: #171d21; }
    dt { color: #dce5df; }
    dd { margin: 0; font-variant-numeric: tabular-nums; color: #9fb7ff; }
    .ok { color: #8fe1a2; }
    .warn { color: #ffd27d; }
    .bad { color: #ff9a9a; }
    code { color: #dce5df; }
  </style>
</head>
<body>
  <main>
    <h1>ICE candidate test</h1>
    <p>This authenticated diagnostic checks whether the browser can gather local, reflexive, and relay candidates for the current MeetingAssist configuration.</p>
    <dl>
      <div class="row"><dt>Local candidates</dt><dd id="host">checking</dd></div>
      <div class="row"><dt>Server-reflexive candidates</dt><dd id="srflx">checking</dd></div>
      <div class="row"><dt>Relay candidates</dt><dd id="relay">checking</dd></div>
      <div class="row"><dt>Status</dt><dd id="status">starting</dd></div>
    </dl>
    <p id="summary">Gathering candidates for up to 8 seconds.</p>
  </main>
  <script>
    const found = { host: false, srflx: false, relay: false }
    const labels = {
      host: document.getElementById('host'),
      srflx: document.getElementById('srflx'),
      relay: document.getElementById('relay'),
      status: document.getElementById('status'),
      summary: document.getElementById('summary')
    }
    function setCandidate(type, value) {
      found[type] = found[type] || value
      labels[type].textContent = found[type] ? 'found' : 'not found'
      labels[type].className = found[type] ? 'ok' : 'warn'
    }
    function finish(status, detail) {
      labels.status.textContent = status
      labels.status.className = found.relay ? 'ok' : 'warn'
      labels.summary.textContent = detail
      for (const type of Object.keys(found)) setCandidate(type, found[type])
    }
    async function run() {
      try {
        const response = await fetch('/client-config', { cache: 'no-store' })
        if (!response.ok) throw new Error('client config unavailable')
        const config = await response.json()
        const pc = new RTCPeerConnection(config.rtcConfiguration || {})
        pc.createDataChannel('ice-test')
        pc.onicecandidate = event => {
          if (!event.candidate) {
            finish('complete', found.relay ? 'Relay connectivity is available.' : 'No relay candidate was gathered in this browser session.')
            pc.close()
            return
          }
          const text = String(event.candidate.candidate || '')
          if (/\styp host(\s|$)/.test(text)) setCandidate('host', true)
          if (/\styp srflx(\s|$)/.test(text)) setCandidate('srflx', true)
          if (/\styp relay(\s|$)/.test(text)) setCandidate('relay', true)
        }
        await pc.setLocalDescription(await pc.createOffer())
        setTimeout(() => {
          if (pc.connectionState !== 'closed') {
            finish('timed out', found.relay ? 'Relay connectivity is available.' : 'No relay candidate was gathered before timeout.')
            pc.close()
          }
        }, 8000)
      } catch (error) {
        labels.status.textContent = 'failed'
        labels.status.className = 'bad'
        labels.summary.textContent = error.message || String(error)
      }
    }
    run()
  </script>
</body>
</html>`

func iceTestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write([]byte(iceTestHTML)); err != nil {
		log.Errorf("Failed to serve ICE test page: %v", err)
	}
}
