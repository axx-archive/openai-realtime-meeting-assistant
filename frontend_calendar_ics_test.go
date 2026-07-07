package main

// Frontend contract pins for card 084 (calendar .ics slice): the per-milestone
// "add to calendar" button in the key-dates timeline and the downloadKeyDateICS
// function that fetches the session-gated /calendar/event.ics endpoint and
// streams the result to a blob download. Same contract-pin style as
// frontend_share_export_test.go — parse index.html, assert the wiring is
// present so a refactor can't silently drop it.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForCalendar(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// downloadKeyDateICS hits the endpoint with encoded params and toasts on both
// success and the unparseable-date path.
func TestDownloadKeyDateICSFunctionWiring(t *testing.T) {
	html := readIndexForCalendar(t)
	body := functionBody(html, "function downloadKeyDateICS")
	if body == "" {
		t.Fatal("downloadKeyDateICS function not found in index.html")
	}
	for _, want := range []string{
		"/calendar/event.ics",
		"encodeURIComponent",
		"showToast",
		"URL.createObjectURL",
		".ics",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("downloadKeyDateICS missing %q", want)
		}
	}
}

// The timeline renders a per-step calendar button wired to the download.
// functionBody can't bound renderCardKeyDates (its `options = {}` default
// param confuses the brace matcher), so slice the region between the two
// function declarations and assert the wiring lives inside it.
func TestRenderCardKeyDatesHasCalendarButton(t *testing.T) {
	html := readIndexForCalendar(t)
	start := strings.Index(html, "function renderCardKeyDates")
	if start == -1 {
		t.Fatal("renderCardKeyDates function not found in index.html")
	}
	end := strings.Index(html[start:], "async function downloadKeyDateICS")
	if end == -1 {
		t.Fatal("downloadKeyDateICS declaration not found after renderCardKeyDates")
	}
	region := html[start : start+end]
	for _, want := range []string{
		"key-date-step__cal",
		"downloadKeyDateICS(card, item)",
	} {
		if !strings.Contains(region, want) {
			t.Errorf("renderCardKeyDates region missing %q", want)
		}
	}
}

// The icon button carries its own style block.
func TestCalendarButtonStylePresent(t *testing.T) {
	html := readIndexForCalendar(t)
	if !strings.Contains(html, ".key-date-step__cal {") {
		t.Error("index.html missing .key-date-step__cal style rule")
	}
}
