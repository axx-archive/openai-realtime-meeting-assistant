package main

import (
	"os"
	"strings"
	"testing"
)

func TestPhoneChatKeepsBubbleMeasureAndRealHitTargets(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)
	phoneStart := strings.Index(html, "@media (max-width: 640px), (max-height: 500px)")
	if phoneStart < 0 {
		t.Fatal("phone media query missing")
	}
	phone := html[phoneStart:]
	for _, want := range []string{
		"#chatTool .scout-chat-msg__stack {\n          max-width: 100%;",
		"gap: 4px;",
		"width: var(--hit-min);\n          min-width: var(--hit-min);\n          height: var(--hit-min);\n          min-height: var(--hit-min);",
		"#chatTool .scout-chat-send::before {\n          background: var(--accent);",
		"#chatTool .stride-dictation-action::before {\n          content: none;",
		"transform: scale(0.96);",
	} {
		if !strings.Contains(phone, want) {
			t.Errorf("phone chat quality contract missing %q", want)
		}
	}

	composerStart := strings.Index(phone, ".scout-chat-form {\n          gap: 4px;")
	composerEnd := strings.Index(phone, "/* mobile goalcard legibility")
	if composerStart < 0 || composerEnd <= composerStart {
		t.Fatal("phone chat composer contract block missing")
	}
	composer := phone[composerStart:composerEnd]
	if strings.Contains(composer, "width: 31px") || strings.Contains(composer, "height: 31px") {
		t.Fatal("phone send target must not shrink below the shared 44px hit minimum")
	}
}
