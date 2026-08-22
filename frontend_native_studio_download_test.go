package main

import (
	"os"
	"strings"
	"testing"
)

func TestNativeStudioDownloadsUseSemanticBridgeBeforeBrowserFallback(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		"function nativeStudioDownload(payload)",
		"bridge.postMessage(JSON.stringify({ type: 'stride.studio.download', version: 1, ...payload }))",
		"kind: 'deck', format: 'pptx'",
		"kind: 'deck', format: 'pdf'",
		"kind: 'document', format: 'pdf'",
		"studioDownloadFileName(landed.pdf.name || state.title, 'report', 'pdf')",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("native Studio download contract missing %q", want)
		}
	}

	powerPointStart := strings.Index(html, "async function exportPowerPoint(button)")
	if powerPointStart < 0 {
		t.Fatal("Deck Studio PowerPoint export is missing")
	}
	powerPointEnd := strings.Index(html[powerPointStart:], "async function exportPDF(button)")
	if powerPointEnd < 0 {
		t.Fatal("Deck Studio PowerPoint export boundary is missing")
	}
	powerPoint := html[powerPointStart : powerPointStart+powerPointEnd]
	bridge := strings.Index(powerPoint, "nativeStudioDownload({")
	browserFetch := strings.Index(powerPoint, "fetch('/artifacts/export-pptx'")
	if bridge < 0 || browserFetch < 0 || bridge > browserFetch {
		t.Fatal("WKWebView must receive the exact PowerPoint request before the browser blob fallback")
	}
}
