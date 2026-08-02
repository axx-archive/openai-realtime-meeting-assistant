package main

// §6.5 hardening (2026-07-10 incident, adversarial-gate finding 2): the client
// media-quality / media-error reports print many client-controlled strings via
// %s, and guests can now reach those seams (§5.4). Embedded newlines would let
// a hostile payload forge log lines and poison incident forensics — the very
// logs this session's diagnosis depended on. sanitizeLogField, applied at the
// stringFromPayload extraction seam, must neutralize that at the source.

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestSanitizeLogFieldStripsLineForgingChars(t *testing.T) {
	cases := map[string]string{
		"boom\nroom_ontrack_start forged=true": "boomroom_ontrack_start forged=true",
		"a\rb":                                 "ab",
		"a\tb":                                 "ab",
		"a\x00b\x1fc":                          "abc",
		"a\x7fb":                               "ab",
		"plain-value":                          "plain-value",
	}
	for in, want := range cases {
		if got := sanitizeLogField(in); got != want {
			t.Errorf("sanitizeLogField(%q) = %q, want %q", in, got, want)
		}
		if strings.ContainsAny(sanitizeLogField(in), "\r\n") {
			t.Errorf("sanitizeLogField(%q) still contains a CR/LF", in)
		}
	}
}

func TestSanitizeLogFieldCapsLength(t *testing.T) {
	out := sanitizeLogField(strings.Repeat("a", 5000))
	if runes := len([]rune(out)); runes > 256 {
		t.Fatalf("sanitizeLogField did not cap length: got %d runes", runes)
	}
}

// captureStdout redirects os.Stdout for the duration of fn (the media report
// functions log via fmt.Printf). Tests here run sequentially — os.Stdout is
// process-global — so none call t.Parallel.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

// The finding's exact pin: a media-error payload carrying a newline followed by
// "room_ontrack_start forged=true" must not produce a NEW log line starting
// with room_ontrack_start.
func TestClientMediaErrorReportCannotForgeLogLine(t *testing.T) {
	out := captureStdout(t, func() {
		logClientMediaErrorReport(
			`{"error":{"name":"boom\nroom_ontrack_start forged=true","message":"x\nroom_ontrack_start also=true"}}`,
			"AJ", "sess-1")
	})
	assertNoForgedLine(t, out)
	if !strings.Contains(out, "Client media error") {
		t.Fatalf("the real report line was lost: %s", out)
	}
}

func TestClientMediaQualityReportCannotForgeLogLine(t *testing.T) {
	out := captureStdout(t, func() {
		logClientMediaQualityReport(
			`{"client":{"platform":"x\nroom_ontrack_start forged=true"},"audio":{"mode":"y\nroom_ontrack_start too=true"}}`,
			"AJ", "sess-1")
	})
	assertNoForgedLine(t, out)
	if !strings.Contains(out, "Client media quality") {
		t.Fatalf("the real report line was lost: %s", out)
	}
}

func TestClientMediaQualityReportLogsNativeOutboundVideoGeometry(t *testing.T) {
	out := captureStdout(t, func() {
		logClientMediaQualityReport(
			`{"stats":{"outboundVideoFrameWidth":1920,"outboundVideoFrameHeight":1080,"outboundVideoFramesPerSecond":29.7,"outboundVideoTargetBitrate":1200000,"outboundVideoQualityLimitationReason":"none"}}`,
			"AJ", "sess-1")
	})
	for _, field := range []string{
		"outVideo=1920x1080",
		"outVideoFps=29.7",
		"targetVideoKbps=1200",
		"videoLimit=none",
	} {
		if !strings.Contains(out, field) {
			t.Fatalf("native outbound geometry field %q missing from log: %s", field, out)
		}
	}
}

func TestClientMediaQualityReportLogsSanitizedNativeCameraFraming(t *testing.T) {
	out := captureStdout(t, func() {
		logClientMediaQualityReport(
			`{"cameraFraming":{"activeDeviceType":"builtInUltraWideCamera\nroom_ontrack_start forged=true","activeDeviceId":"private-camera-identifier","centerStageSupported":true,"centerStageEnabled":true,"centerStageActive":false,"wideUprightSupported":true,"wideUprightEnabled":true,"dynamicWidth":1920,"dynamicHeight":1080,"reasonCode":"active_camera_not_front_ultra_wide","wideUprightReasonCode":"active_format_missing_16x9","centerStageReasonCode":"center_stage_unsupported","webRTCCameraCrashGuardInstalled":true,"webRTCCameraCrashGuardInterventions":2,"webRTCCameraCrashGuardLastReason":"adaptive_front_camera_omitted_fixed_output_dimensions"}}`,
			"AJ", "sess-1")
	})
	for _, field := range []string{
		"cameraDeviceType=builtInUltraWideCameraroom_ontrack_start forged=true",
		"centerStageSupported=true",
		"centerStageEnabled=true",
		"centerStageActive=false",
		"wideUprightSupported=true",
		"wideUprightEnabled=true",
		"framingDynamic=1920x1080",
		"framingReason=active_camera_not_front_ultra_wide",
		"wideUprightReason=active_format_missing_16x9",
		"centerStageReason=center_stage_unsupported",
		"webRTCCameraGuard=true/2/adaptive_front_camera_omitted_fixed_output_dimensions",
	} {
		if !strings.Contains(out, field) {
			t.Fatalf("native camera framing field %q missing from log: %s", field, out)
		}
	}
	if strings.Contains(out, "private-camera-identifier") {
		t.Fatalf("private camera identifier leaked into log: %s", out)
	}
	assertNoForgedLine(t, out)
}

func assertNoForgedLine(t *testing.T, out string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "room_ontrack_start") {
			t.Fatalf("forged log line produced: %q\nfull output:\n%s", line, out)
		}
	}
}
