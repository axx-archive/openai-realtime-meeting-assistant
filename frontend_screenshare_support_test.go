package main

// 2026-07-10: screen sharing is impossible on iOS Safari (no
// navigator.mediaDevices.getDisplayMedia). The control used to set native
// `disabled`, so a tap was a silent no-op — and startScreenShare's catch
// buried real capture failures in the tiny setLog status line while the guards
// beside it raised proper error toasts. These pins hold the fix: an
// aria-disabled control that stays clickable (a native disabled button eats
// the tap) so the guard toast fires, iOS-named unsupported copy, and a
// cancel-silent / failure-loud catch.

import (
	"os"
	"strings"
	"testing"
)

func readIndexHTMLForShareSupport(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}

// The unsupported control is marked aria-disabled, never natively disabled, so
// a tap still reaches the click handler (and its guard toast). The same state
// mirrors onto the board-dock forwarder, and the dimmed disabled look is kept
// via an aria-disabled CSS rule for both control families.
func TestIndexScreenShareUnsupportedStaysClickable(t *testing.T) {
	html := readIndexHTMLForShareSupport(t)

	helper := functionBody(html, "function applyScreenShareSupportState()")
	if helper == "" {
		t.Fatal("applyScreenShareSupportState helper is missing")
	}
	for _, want := range []string{
		"screenShareButton",
		"boardDockShare",
		"setAttribute('aria-disabled', 'true')",
		"removeAttribute('aria-disabled')",
	} {
		if !strings.Contains(helper, want) {
			t.Errorf("applyScreenShareSupportState is missing %q", want)
		}
	}

	// the in-room enable path must NOT natively disable the unsupported button
	// (a native disabled button swallows the tap); it ungates and defers the
	// look to the aria-disabled helper
	if strings.Contains(html, "screenShareButton.disabled = !screenShareSupported") {
		t.Error("the unsupported button must not be natively disabled (a disabled button eats the tap)")
	}
	if !strings.Contains(html, "applyScreenShareSupportState()") {
		t.Error("applyScreenShareSupportState must be wired in")
	}

	// the dimmed disabled look is preserved for aria-disabled controls
	if !strings.Contains(html, `.btn[aria-disabled="true"]`) {
		t.Error("aria-disabled buttons must keep the disabled visual")
	}
	if !strings.Contains(html, `.board-dock-icon[aria-disabled="true"]`) {
		t.Error("aria-disabled board-dock controls must keep the disabled visual")
	}
}

// Tapping the unsupported control raises the SAME addAssistantMessage error
// toast the join guards use, and the copy names iOS Safari.
func TestIndexScreenShareUnsupportedToastMatchesGuards(t *testing.T) {
	html := readIndexHTMLForShareSupport(t)

	body := functionBody(html, "async function startScreenShare()")
	if body == "" {
		t.Fatal("could not extract startScreenShare body")
	}
	guardAt := strings.Index(body, "if (!screenShareSupported) {")
	if guardAt == -1 {
		t.Fatal("startScreenShare lost its unsupported guard")
	}
	guard := body[guardAt:]
	if cut := strings.Index(guard, "try {"); cut != -1 {
		guard = guard[:cut]
	}
	for _, want := range []string{
		"iOS Safari",
		"addAssistantMessage({ kind: 'error', text: message })",
		"setLog(message)",
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("the unsupported guard is missing %q", want)
		}
	}
}

// The catch keeps setLog for detail but ALSO raises the guards' error toast for
// a genuine capture failure, while a user-cancel (NotAllowedError / AbortError,
// not reliably distinguishable from a real permission block) stays silent.
func TestIndexScreenShareCaptureFailureToast(t *testing.T) {
	html := readIndexHTMLForShareSupport(t)

	body := functionBody(html, "async function startScreenShare()")
	if body == "" {
		t.Fatal("could not extract startScreenShare body")
	}
	catchAt := strings.LastIndex(body, "} catch (error) {")
	if catchAt == -1 {
		t.Fatal("startScreenShare lost its catch")
	}
	catch := body[catchAt:]
	for _, want := range []string{
		"setLog(error.message || String(error))",
		"error?.name",
		"'NotAllowedError'",
		"'AbortError'",
		"addAssistantMessage({ kind: 'error', text: message })",
	} {
		if !strings.Contains(catch, want) {
			t.Errorf("the startScreenShare catch is missing %q", want)
		}
	}
	// cancel stays silent: the toast is gated behind a NOT-cancel check
	if !strings.Contains(catch, "if (name !== 'NotAllowedError' && name !== 'AbortError') {") {
		t.Error("the capture-failure toast must be gated so user-cancel stays silent")
	}
}
