package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The 2026-07-05 production outage class: index.html is one giant top-level
// script, so a function called during the BOOT render pass that touches a
// let/const declared thousands of lines later hits the temporal dead zone,
// and the thrown ReferenceError aborts the ENTIRE remaining script body —
// every later declaration stays uninitialized and the app is half-dead
// (palette gone, meetingRecord/scoutCaptionTimer cascades). Grep pins cannot
// see eval order, so this guard pins the specific repaired landmine and the
// rule that repaired it.
func TestIndexRenderSidecarProbeStateHoists(t *testing.T) {
	html := readIndexHTMLForBootTDZ(t)

	// The probe state must stay var (hoisted + initialized undefined): the
	// boot render pass reaches probeRenderSidecar via renderArtifacts →
	// renderArtifactDetail long before the declaration line executes.
	for _, name := range []string{"renderSidecarReady", "renderSidecarProbe"} {
		if regexp.MustCompile(`(?m)^\s*(let|const)\s+` + name + `\b`).MatchString(html) {
			t.Fatalf("%s is declared with let/const — a boot-order TDZ landmine (see the 2026-07-05 outage); declare it with var", name)
		}
		if !regexp.MustCompile(`(?m)^\s*var\s+` + name + `\b`).MatchString(html) {
			t.Fatalf("%s var declaration is missing from index.html", name)
		}
	}
}

// The boot pass itself must not grow new same-class landmines: every function
// invoked from the boot block must not read a let/const that is declared
// AFTER the boot block line. This is a heuristic (single-file, top-level
// declarations only), tuned to zero false positives on the current tree —
// if it fires, the cheapest fix is `var`, the better fix is lazy init.
func TestIndexBootPassAvoidsLateDeclarations(t *testing.T) {
	html := readIndexHTMLForBootTDZ(t)
	lines := strings.Split(html, "\n")

	// Locate the boot block by its anchor call, renderArtifacts() — the pass
	// that detonated the 2026-07-05 landmine.
	bootLine := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "renderArtifacts()" {
			bootLine = i
			break
		}
	}
	if bootLine < 0 {
		t.Fatal("boot anchor renderArtifacts() not found — update this guard alongside the boot block")
	}

	// Top-level let/const declared after the boot block. Locals and template
	// -literal code share the file's indentation, so disambiguate by
	// uniqueness: a name declared more than once anywhere is a repeating
	// local, a true top-level singleton declares exactly once.
	// Two combined discriminators: top-level statements in this file sit at
	// exactly 6 spaces (locals sit deeper), and template-literal code that
	// shares the indentation reuses common names — so require BOTH the
	// 6-space indent and a unique name across every declaration in the file.
	topDeclRe := regexp.MustCompile(`^\x20{6}(?:let|const)\s+([A-Za-z_$][\w$]*)`)
	anyDeclRe := regexp.MustCompile(`^\s+(?:let|const)\s+([A-Za-z_$][\w$]*)`)
	declCount := map[string]int{}
	for _, line := range lines {
		if m := anyDeclRe.FindStringSubmatch(line); m != nil {
			declCount[m[1]]++
		}
	}
	late := map[string]int{}
	for i := bootLine; i < len(lines); i++ {
		if m := topDeclRe.FindStringSubmatch(lines[i]); m != nil && declCount[m[1]] == 1 {
			late[m[1]] = i + 1
		}
	}

	// Functions the boot render pass reaches synchronously today. Keep this
	// list small and honest: the direct render entry points near the anchor.
	bootReach := []string{"renderArtifacts", "renderArtifactDetail", "probeRenderSidecar", "renderBoard"}
	body := map[string]string{}
	fnRe := regexp.MustCompile(`(?m)^\s*(?:async\s+)?function\s+([A-Za-z_$][\w$]*)\s*\(`)
	fnStarts := fnRe.FindAllStringSubmatchIndex(html, -1)
	for idx, m := range fnStarts {
		name := html[m[2]:m[3]]
		end := len(html)
		if idx+1 < len(fnStarts) {
			end = fnStarts[idx+1][0]
		}
		if _, want := body[name]; !want {
			body[name] = html[m[0]:end]
		}
	}
	for _, fn := range bootReach {
		src, ok := body[fn]
		if !ok {
			continue
		}
		for name, declLine := range late {
			if regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(src) {
				t.Errorf("boot-reachable %s references %q declared at line %d (after the boot block) — TDZ on load; use var or lazy init", fn, name, declLine)
			}
		}
	}
}

func readIndexHTMLForBootTDZ(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}
