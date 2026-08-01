package main

import (
	"os"
	"strings"
	"testing"
)

// The dropdown was the last control in the shell that opened a system popup.
// The closed select was already ours — appearance: none, our chevron, our
// tokens — but the open popup belonged to the OS, and it was the first thing
// anyone touched on the sign-in gate. These pins hold the designed popup's
// contract: pointer users get the warm-glass listbox, touch keeps the platform
// picker, and the real <select> keeps the value, the form semantics, and the
// change event other code listens for.
func TestIndexDesignedDropdown(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	index := string(raw)

	for _, wiring := range []string{
		// The panel is the same glass as every other floating surface.
		".stride-select {",
		".stride-select__option {",
		"backdrop-filter: var(--glass-blur-chrome);",
		// Enhancement is delegated, so selects created later (the palette's
		// package picker) are covered without registration.
		"function openStrideSelect(select)",
		"function closeStrideSelect(refocus)",
		"function strideSelectFor(target)",
		// The real <select> stays authoritative: value set + change dispatched.
		"select.dispatchEvent(new Event('change', { bubbles: true }))",
		// Fine pointers only — on touch the platform picker is better than
		// anything we would build.
		"const finePointer = window.matchMedia('(hover: hover) and (pointer: fine)')",
		// The hidden a11y shim and disabled selects are never intercepted.
		`target.closest('[aria-hidden="true"]')`,
		"target.disabled",
		"data-native-select",
		// Keyboard contract: popup-opening keys open ours, Escape returns
		// focus without changing the value, type-ahead works.
		"event.key === 'Escape'",
		"openState.typeahead",
		"panel.setAttribute('role', 'listbox')",
		// WebKit opens the native picker even on a cancelled mousedown, so
		// suppression is structural: the select leaves the pointer path and
		// the click resolves from its host. If these go, Safari regresses to
		// the system popup while every Chromium check stays green.
		"pointer-events: none;",
		"function resolveStrideSelect(target)",
		"target.closest(':has(> select)')",
	} {
		if !strings.Contains(index, wiring) {
			t.Fatalf("the designed dropdown contract is not wired: missing %q", wiring)
		}
	}

	// Motion respects the reduced-motion contract like every other surface.
	if !strings.Contains(index, ".stride-select { transition: none; }") {
		t.Fatal("the dropdown must stand still under prefers-reduced-motion")
	}
}
