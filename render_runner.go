package main

// render_runner.go — the render-runner sidecar core (packaging OS §4 "PDF
// export — pulled forward by founder decision", Wave 3 item 14b). It is the
// codex runner's sibling on the same chassis: the same Go binary in a new
// mode (stage B wires a -render-runner flag in main.go to runRenderRunnerLoop
// — main.go is NOT touched here), a file-per-job queue at data/render-jobs
// (claim lexically-first, one at a time), and an authenticated result POST
// back to the OS with Bearer BONFIRE_RUNNER_TOKEN.
//
// One job type: export_pdf {artifactId, kind: deck|paper}.
//   - deck: artifact HTML → chromium headless print-to-pdf (the layered
//     print) → pdftoppm -jpeg -r 144 → pure-Go JPEG→PDF reassembly
//     (jpeg_pdf.go). THE FLATTEN LAW IS NON-NEGOTIABLE: the layered print
//     never ships; the flattened raster PDF is the deliverable.
//   - paper ("The Talk" / "The Wall", and server-rendered markdown research
//     reports via renderResearchReportPrintHTML): text-native, no blends —
//     chromium print-to-pdf DIRECT, no flatten.
// Both kinds also persist the per-page JPEGs (free from pdftoppm) to the
// purpose-specific render_queue volume and reference them in the callback —
// Wave 5's vision slide juries consume exactly these images. The renderer
// never mounts the company-brain meeting_data volume.
//
// Graceful absence mirrors the codex sidecar: when the sidecar (or its
// chromium/pdftoppm toolchain) is missing, jobs fail with a clear operator
// message and the readiness snapshot reports a missing/stale heartbeat so
// the OS side can surface "render sidecar not available". Nothing here needs
// an API key — the runner degrades identically keyless.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	renderJobTypeExportPDF = "export_pdf"

	renderJobKindDeck  = "deck"
	renderJobKindPaper = "paper"

	renderJobStatusQueued   = "queued"
	renderJobStatusRunning  = "running"
	renderJobStatusComplete = "complete"
	renderJobStatusFailed   = "failed"

	defaultRenderRunnerPollInterval = 2 * time.Second
	defaultRenderRunnerStaleAfter   = 2 * time.Minute
	defaultRenderCanaryInterval     = time.Minute
	defaultRenderCanaryFailureRetry = 15 * time.Second
	defaultRenderCanaryTimeout      = 45 * time.Second
	defaultRenderExecTimeout        = 3 * time.Minute
	defaultRenderMaxPDFBytes        = 64 << 20
	// A self-contained deck may carry up to six generated images as data URIs.
	// Keep the queue bounded while leaving enough headroom for that authored
	// contract plus HTML/CSS and presenter notes.
	defaultRenderMaxHTMLBytes       = 32 << 20
)

type renderRunnerJob struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	ArtifactID  string            `json:"artifact_id"`
	Kind        string            `json:"kind"`
	HTML        string            `json:"html"`
	Title       string            `json:"title,omitempty"`
	Status      string            `json:"status"`
	CreatedAt   time.Time         `json:"created_at"`
	StartedAt   time.Time         `json:"started_at,omitempty"`
	CompletedAt time.Time         `json:"completed_at,omitempty"`
	Attempts    int               `json:"attempts"`
	RunnerID    string            `json:"runner_id,omitempty"`
	Error       string            `json:"error,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type renderRunnerJobStore struct {
	dir string
}

// renderRunnerCanaryResult is deliberately body- and secret-free. The runner
// emits it only after the exact production pipeline has printed minimal HTML
// to PDF and rasterized that PDF with pdftoppm inside its current container.
// Release verification treats this as an acceptance gate, not telemetry.
type renderRunnerCanaryResult struct {
	OK        bool
	CheckedAt time.Time
	PageCount int
	PDFBytes  int
	ErrorCode string
}

// renderRunnerCallbackPayload is what the sidecar POSTs to
// /internal/render/jobs/result (handler lands in stage B). The flattened PDF
// rides base64 in the payload — mirroring how codex results ride in the
// callback body — AND is persisted to the dedicated render_queue volume with
// its path in the payload, so the OS side can pick whichever transport the
// blob store prefers. Page JPEGs are render-volume refs only.
type renderRunnerCallbackPayload struct {
	JobID          string            `json:"job_id"`
	ArtifactID     string            `json:"artifact_id"`
	Kind           string            `json:"kind,omitempty"`
	Status         string            `json:"status"`
	PDFBase64      string            `json:"pdf_base64,omitempty"`
	PDFPath        string            `json:"pdf_path,omitempty"`
	PageJPEGPaths  []string          `json:"page_jpeg_paths,omitempty"`
	PageCount      int               `json:"page_count,omitempty"`
	Flattened      bool              `json:"flattened,omitempty"`
	DeckSinglePage bool              `json:"deck_single_page,omitempty"`
	Error          string            `json:"error,omitempty"`
	RunnerEvidence string            `json:"runner_evidence,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type renderExecConfig struct {
	ChromiumBin  string
	PdftoppmBin  string
	Timeout      time.Duration
	MaxPDFBytes  int64
	MaxHTMLBytes int64
}

type renderExportPDFResult struct {
	PDFBytes      []byte
	PDFPath       string
	PageJPEGPaths []string
	PageCount     int
	Flattened     bool
	// DeckSinglePage flags a deck that flattened to exactly one page — a
	// multi-slide deck rendering one page is the pagination defect, so it is
	// disclosed on the callback rather than shipped silently as a valid export.
	DeckSinglePage bool
}

// renderExecInvocation + the seam variable let tests pin the exact commands
// the flatten law dictates without chromium/pdftoppm on CI, mirroring the
// runCodexExecCommand seam.
type renderExecInvocation struct {
	Bin  string
	Args []string
	Dir  string
}

var runRenderExecCommand = runRenderExecCommandContext

func renderRunnerQueuePath() string {
	if path := strings.TrimSpace(os.Getenv("BONFIRE_RENDER_QUEUE_PATH")); path != "" {
		return filepath.Clean(path)
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "render-jobs")
}

func renderRunnerHeartbeatPath() string {
	if path := strings.TrimSpace(os.Getenv("BONFIRE_RENDER_HEARTBEAT_PATH")); path != "" {
		return filepath.Clean(path)
	}
	return filepath.Join(filepath.Dir(renderRunnerQueuePath()), "render-runner-heartbeat.json")
}

func renderRunnerPollInterval() time.Duration {
	return durationEnv("BONFIRE_RENDER_RUNNER_POLL_INTERVAL", defaultRenderRunnerPollInterval, 250*time.Millisecond)
}

func renderExecConfigFromEnv() renderExecConfig {
	return renderExecConfig{
		ChromiumBin:  getenvDefault("RENDER_CHROMIUM_BIN", "chromium"),
		PdftoppmBin:  getenvDefault("RENDER_PDFTOPPM_BIN", "pdftoppm"),
		Timeout:      durationEnv("BONFIRE_RENDER_TIMEOUT", defaultRenderExecTimeout, 10*time.Second),
		MaxPDFBytes:  int64(positiveIntEnv("BONFIRE_RENDER_MAX_PDF_BYTES", defaultRenderMaxPDFBytes)),
		MaxHTMLBytes: int64(positiveIntEnv("BONFIRE_RENDER_MAX_HTML_BYTES", defaultRenderMaxHTMLBytes)),
	}
}

func normalizeRenderJobKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case renderJobKindPaper:
		return renderJobKindPaper
	default:
		return renderJobKindDeck
	}
}

// serverRenderKindForArtifact owns the print path: the flatten law is
// server-side law, never a client choice. Paper (text-native direct print,
// no flatten) is only for artifacts that declare themselves paper-kit
// documents — packaging_studio stamps paperKit=true on The Talk / The Wall
// it files. Everything else is a deck and always flattens.
func serverRenderKindForArtifact(artifact meetingMemoryEntry) string {
	if strings.EqualFold(strings.TrimSpace(artifact.Metadata["paperKit"]), "true") {
		return renderJobKindPaper
	}
	return renderJobKindDeck
}

func newRenderRunnerJobStore(dir string) *renderRunnerJobStore {
	return &renderRunnerJobStore{dir: filepath.Clean(strings.TrimSpace(dir))}
}

func (store *renderRunnerJobStore) enqueue(job renderRunnerJob) (renderRunnerJob, error) {
	if store == nil || strings.TrimSpace(store.dir) == "" {
		return renderRunnerJob{}, fmt.Errorf("render runner queue path is not configured")
	}
	if strings.TrimSpace(job.ID) == "" {
		job.ID = newRenderRunnerJobID()
	}
	if strings.TrimSpace(job.Type) == "" {
		job.Type = renderJobTypeExportPDF
	}
	if strings.TrimSpace(job.Status) == "" {
		job.Status = renderJobStatusQueued
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	job.Kind = normalizeRenderJobKind(job.Kind)
	if job.Metadata == nil {
		job.Metadata = map[string]string{}
	}

	if err := os.MkdirAll(store.dir, 0o755); err != nil {
		return renderRunnerJob{}, fmt.Errorf("create render runner queue: %w", err)
	}
	if err := writeRenderRunnerJobAtomically(store.jobPath(job.ID), job); err != nil {
		return renderRunnerJob{}, err
	}
	return job, nil
}

func (store *renderRunnerJobStore) claimNext(runnerID string) (*renderRunnerJob, error) {
	if store == nil || strings.TrimSpace(store.dir) == "" {
		return nil, fmt.Errorf("render runner queue path is not configured")
	}
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read render runner queue: %w", err)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, err := store.read(entry.Name())
		if err != nil {
			return nil, err
		}
		if job.Status != renderJobStatusQueued {
			continue
		}
		now := time.Now().UTC()
		job.Status = renderJobStatusRunning
		job.StartedAt = now
		job.Attempts++
		job.RunnerID = runnerID
		if job.Metadata == nil {
			job.Metadata = map[string]string{}
		}
		job.Metadata["claimedAt"] = now.Format(time.RFC3339Nano)
		job.Metadata["runnerId"] = runnerID
		if err := store.update(*job); err != nil {
			return nil, err
		}
		return job, nil
	}

	return nil, nil
}

func (store *renderRunnerJobStore) update(job renderRunnerJob) error {
	if strings.TrimSpace(job.ID) == "" {
		return fmt.Errorf("render runner job id is required")
	}
	return writeRenderRunnerJobAtomically(store.jobPath(job.ID), job)
}

// writeRenderRunnerJobAtomically is deliberately group-readable/writable.
// The app process enqueues jobs while the isolated renderer runs as uid/gid
// 65532 in a different container. The render_queue jobs directory is setgid
// to 65532 by render-queue-init, so 0660 preserves the shared group across
// every atomic replacement without making untrusted render payloads public.
func writeRenderRunnerJobAtomically(path string, job renderRunnerJob) error {
	rawJSON, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return fmt.Errorf("encode render runner job: %w", err)
	}
	rawJSON = append(rawJSON, '\n')
	if err := writeFileAtomicallyForCanonicalMode(path, rawJSON, 0o660); err != nil {
		return fmt.Errorf("persist render runner job: %w", err)
	}
	return nil
}

func (store *renderRunnerJobStore) read(filename string) (*renderRunnerJob, error) {
	path := filepath.Join(store.dir, filepath.Base(filename))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read render runner job: %w", err)
	}
	var job renderRunnerJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, fmt.Errorf("decode render runner job %s: %w", filepath.Base(filename), err)
	}
	return &job, nil
}

func (store *renderRunnerJobStore) jobPath(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = newRenderRunnerJobID()
	}
	return filepath.Join(store.dir, id+".json")
}

// renderJobResultsDir is the per-job render-volume output directory. It lives
// inside the queue directory (which both containers mount via render_queue),
// and claimNext skips directories, so results never masquerade as jobs.
func renderJobResultsDir(queueDir string, jobID string) string {
	return filepath.Join(queueDir, strings.TrimSpace(jobID)+"-out")
}

func newRenderRunnerJobID() string {
	return fmt.Sprintf("render-job-%d-%d", time.Now().UTC().UnixNano(), os.Getpid())
}

// enqueueRenderExportPDFJob is the clearly-named stage-B seam: the trigger
// route (main.go — deliberately not touched here) calls this with the
// artifact's print HTML — the deck/paper body itself, or the branded print
// document a markdown research report converts to — to queue an export_pdf
// job for the render-runner sidecar. It returns the queued job so the caller
// can stamp runnerJobId metadata on the artifact, exactly like
// enqueueCodexAgentThreadJob does.
func enqueueRenderExportPDFJob(artifactID string, kind string, html string, title string) (renderRunnerJob, error) {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return renderRunnerJob{}, fmt.Errorf("artifact id is required for PDF export")
	}
	if strings.TrimSpace(html) == "" {
		return renderRunnerJob{}, fmt.Errorf("artifact HTML body is required for PDF export")
	}
	if len(html) > defaultRenderMaxHTMLBytes {
		return renderRunnerJob{}, fmt.Errorf("artifact HTML body is %d bytes, above the %d-byte render queue limit", len(html), defaultRenderMaxHTMLBytes)
	}
	store := newRenderRunnerJobStore(renderRunnerQueuePath())
	return store.enqueue(renderRunnerJob{
		Type:       renderJobTypeExportPDF,
		ArtifactID: artifactID,
		Kind:       normalizeRenderJobKind(kind),
		HTML:       html,
		Title:      strings.TrimSpace(title),
		Metadata: map[string]string{
			"workerBoundary": "render_sidecar_queue",
		},
	})
}

// runRenderRunnerLoop is the sidecar entrypoint. Stage B wires the
// -render-runner flag in main.go to it. The former Codex runner entrypoint is
// deliberately absent from the E9 candidate.
func runRenderRunnerLoop(ctx context.Context) error {
	store := newRenderRunnerJobStore(renderRunnerQueuePath())
	runnerID := renderRunnerID()
	pollInterval := renderRunnerPollInterval()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var canary renderRunnerCanaryResult
	var nextCanary time.Time

	log.Infof("Render runner started id=%s queue=%s poll=%s", runnerID, store.dir, pollInterval)
	for {
		now := time.Now().UTC()
		if nextCanary.IsZero() || !now.Before(nextCanary) {
			var err error
			canary, err = runRenderRunnerCanary(ctx, renderExecConfigFromEnv())
			if err != nil {
				log.Errorf("Render runner canary failed code=%s: %v", canary.ErrorCode, err)
				nextCanary = time.Now().UTC().Add(defaultRenderCanaryFailureRetry)
			} else {
				nextCanary = time.Now().UTC().Add(defaultRenderCanaryInterval)
			}
		}
		if err := writeRenderRunnerHeartbeat(runnerID, canary); err != nil {
			log.Errorf("Render runner heartbeat failed: %v", err)
		}
		// Never claim user work while the exact print/raster pipeline is
		// unhealthy. This keeps readiness, release acceptance, and work-serving
		// authority on the same fail-closed fact.
		if !canary.OK {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
			continue
		}
		job, err := store.claimNext(runnerID)
		if err != nil {
			log.Errorf("Render runner queue claim failed: %v", err)
		} else if job != nil {
			processRenderRunnerJob(ctx, store, *job)
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func processRenderRunnerJob(ctx context.Context, store *renderRunnerJobStore, job renderRunnerJob) {
	cfg := renderExecConfigFromEnv()
	now := time.Now().UTC()
	kind := normalizeRenderJobKind(job.Kind)
	runningMetadata := map[string]string{
		"status":       renderJobStatusRunning,
		"renderRunner": "executing",
		"renderKind":   kind,
		"runnerJobId":  job.ID,
		"runnerId":     job.RunnerID,
		"startedAt":    firstNonEmptyString(job.StartedAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)),
	}
	_ = sendRenderRunnerCallback(ctx, renderRunnerCallbackPayload{
		JobID:      job.ID,
		ArtifactID: job.ArtifactID,
		Kind:       kind,
		Status:     renderJobStatusRunning,
		Metadata:   runningMetadata,
	})

	job.Status = renderJobStatusRunning
	job.Metadata = mergeStringMaps(job.Metadata, runningMetadata)
	if err := store.update(job); err != nil {
		log.Errorf("Render runner could not persist running job %s: %v", job.ID, err)
	}

	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	result, err := executeRenderExportPDF(runCtx, cfg, job, renderJobResultsDir(store.dir, job.ID))
	completedAt := time.Now().UTC()
	if err != nil {
		job.Status = renderJobStatusFailed
		job.CompletedAt = completedAt
		job.Error = err.Error()
		job.Metadata = mergeStringMaps(job.Metadata, map[string]string{
			"status":       renderJobStatusFailed,
			"renderRunner": "failed",
			"completedAt":  completedAt.Format(time.RFC3339Nano),
			"error":        err.Error(),
		})
		if updateErr := store.update(job); updateErr != nil {
			log.Errorf("Render runner could not persist failed job %s: %v", job.ID, updateErr)
		}
		_ = sendRenderRunnerCallback(ctx, renderRunnerCallbackPayload{
			JobID:          job.ID,
			ArtifactID:     job.ArtifactID,
			Kind:           kind,
			Status:         renderJobStatusFailed,
			Error:          err.Error(),
			RunnerEvidence: renderRunnerCommandEvidence(cfg, result),
			Metadata:       job.Metadata,
		})
		return
	}

	job.Status = renderJobStatusComplete
	job.CompletedAt = completedAt
	job.Metadata = mergeStringMaps(job.Metadata, map[string]string{
		"status":       renderJobStatusComplete,
		"renderRunner": "complete",
		"completedAt":  completedAt.Format(time.RFC3339Nano),
		"pageCount":    strconv.Itoa(result.PageCount),
		"flattened":    strconv.FormatBool(result.Flattened),
		"pdfBytes":     strconv.Itoa(len(result.PDFBytes)),
		"pdfPath":      result.PDFPath,
	})
	if err := store.update(job); err != nil {
		log.Errorf("Render runner could not persist completed job %s: %v", job.ID, err)
	}

	if err := sendRenderRunnerCallback(ctx, renderRunnerCallbackPayload{
		JobID:          job.ID,
		ArtifactID:     job.ArtifactID,
		Kind:           kind,
		Status:         renderJobStatusComplete,
		PDFBase64:      base64.StdEncoding.EncodeToString(result.PDFBytes),
		PDFPath:        result.PDFPath,
		PageJPEGPaths:  result.PageJPEGPaths,
		PageCount:      result.PageCount,
		Flattened:      result.Flattened,
		DeckSinglePage: result.DeckSinglePage,
		RunnerEvidence: renderRunnerCommandEvidence(cfg, result),
		Metadata:       job.Metadata,
	}); err != nil {
		log.Errorf("Render runner callback failed for job %s: %v", job.ID, err)
	}
}

// renderPrintCSP is the meta-compatible defense-in-depth subset. The primary
// policy is delivered as an HTTP response header by startRenderPrintServer;
// that header can also enforce sandboxing, which browsers ignore in a meta
// policy. Inline scripts remain available for deck layout, but every fetch,
// child document, plugin, form, base URL, and embedding path is denied.
const renderPrintCSP = "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; media-src data:; font-src data:; connect-src 'none'; frame-src 'none'; object-src 'none'; worker-src 'none'; manifest-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// renderPrintHeaderCSP is served over a fresh loopback-only origin for every
// render. `sandbox allow-scripts` preserves authored layout scripts while
// withholding same-origin, forms, popups, downloads, modals, pointer lock,
// storage, and top-navigation privileges. navigate-to is retained as an
// additional policy on Chromium versions that implement it.
const renderPrintHeaderCSP = renderPrintCSP + "; sandbox allow-scripts; navigate-to 'none'"

type renderPrintServer struct {
	URL   string
	close func() error
}

func (server renderPrintServer) Close() error {
	if server.close == nil {
		return nil
	}
	return server.close()
}

// startRenderPrintServer removes file:// from the trust boundary entirely.
// A random, one-document HTTP origin exposes only the provided HTML; local
// paths, /proc, sibling jobs, directory indexes, and arbitrary loopback URLs
// are never served. The listener exists only for the duration of one print.
func startRenderPrintServer(html string) (renderPrintServer, error) {
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return renderPrintServer{}, fmt.Errorf("create render origin nonce: %w", err)
	}
	path := "/render/" + base64.RawURLEncoding.EncodeToString(nonce[:])
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return renderPrintServer{}, fmt.Errorf("start isolated render origin: %w", err)
	}

	server := &http.Server{
		ReadHeaderTimeout: 2 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", renderPrintHeaderCSP)
			w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), display-capture=(), usb=(), payment=()")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodGet || r.URL.Path != path || r.URL.RawQuery != "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(html))
		}),
	}
	done := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(done)
	}()

	return renderPrintServer{
		URL: "http://" + listener.Addr().String() + path,
		close: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := server.Shutdown(ctx)
			<-done
			return err
		},
	}, nil
}

// injectRenderPrintCSP pins renderPrintCSP into untrusted artifact HTML before
// chromium prints it, mirroring the header the sandboxed render route sends.
// The meta tag lands right after <head> when one exists (case-insensitive),
// else after <html>, else it is prepended — in every case the HTML parser
// places a pre-body <meta> into the document head, and a meta CSP applies to
// everything after it, which is the whole untrusted body.
func injectRenderPrintCSP(html string) string {
	meta := `<meta http-equiv="Content-Security-Policy" content="` + renderPrintCSP + `">`
	return insertIntoDocumentHead(html, meta)
}

// insertIntoDocumentHead places snippet right after the opening <head> (or, when
// there is none, after <html>, else prepends it) — the HTML parser lands a
// pre-body node into the document head in every case. Shared by the CSP meta and
// the deck print-CSS fallback so both use the identical, <header>-safe seam.
func insertIntoDocumentHead(html string, snippet string) string {
	lower := strings.ToLower(html)
	for _, tag := range []string{"<head", "<html"} {
		start := strings.Index(lower, tag)
		if start < 0 {
			continue
		}
		// Guard against <header>/<html-ish custom tags: the next byte must
		// close or continue the tag itself, not extend its name.
		if next := start + len(tag); next < len(lower) && lower[next] != '>' && lower[next] != ' ' && lower[next] != '\t' && lower[next] != '\n' && lower[next] != '\r' {
			continue
		}
		end := strings.IndexByte(lower[start:], '>')
		if end < 0 {
			continue
		}
		insert := start + end + 1
		return html[:insert] + snippet + html[insert:]
	}
	// No <head>/<html> tag: a minimal HTML5 deck legitimately opens with
	// <!doctype html> followed directly by <style>/<section>. Insert AFTER the
	// doctype — a prefix prepend un-documents the file, and ship_compile's
	// html_deck validation ("must start with <!doctype html") then rejects the
	// compiled deck (the live Ember run's block).
	leading := len(html) - len(strings.TrimLeft(html, " \t\r\n"))
	if strings.HasPrefix(lower[leading:], "<!doctype") {
		if end := strings.IndexByte(html[leading:], '>'); end >= 0 {
			insert := leading + end + 1
			return html[:insert] + snippet + html[insert:]
		}
	}
	return snippet + html
}

// injectRenderDeckPrintCSS is the pagination safety net: a deck flattens to one
// page when chromium has no @page/@media-print rules to paginate off. ship_deck
// now embeds the chassis, but an authored deck that dropped it (or a legacy
// deck) still needs to render every slide — so when the HTML declares no @page
// rule we inject the chassis print block into the head. Idempotent: a deck that
// already declares @page is left untouched, so the author's own geometry wins.
func injectRenderDeckPrintCSS(html string) string {
	if strings.Contains(strings.ToLower(html), "@page") {
		return html
	}
	return insertIntoDocumentHead(html, "<style>\n"+packagingDeckPrintCSS()+"\n</style>")
}

// resolveRenderBinary locates one toolchain binary, failing with the clear
// operator message the OS side surfaces as "render sidecar not available".
func resolveRenderBinary(label string, bin string, envName string) (string, error) {
	bin = strings.TrimSpace(bin)
	if bin == "" {
		return "", fmt.Errorf("render sidecar not available: no %s binary is configured (set %s)", label, envName)
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("render sidecar not available: %s binary %q was not found — install it in the render-runner image (Dockerfile.render) or point %s at it", label, bin, envName)
	}
	return path, nil
}

// executeRenderExportPDF runs the export_pdf pipeline for one claimed job and
// persists the outputs to resultsDir on the shared volume.
func executeRenderExportPDF(ctx context.Context, cfg renderExecConfig, job renderRunnerJob, resultsDir string) (renderExportPDFResult, error) {
	if jobType := strings.TrimSpace(job.Type); jobType != renderJobTypeExportPDF {
		return renderExportPDFResult{}, fmt.Errorf("unknown render job type %q (the render runner handles %s)", jobType, renderJobTypeExportPDF)
	}
	if strings.TrimSpace(job.HTML) == "" {
		return renderExportPDFResult{}, fmt.Errorf("render job %s carries no artifact HTML to print", job.ID)
	}
	if cfg.MaxHTMLBytes <= 0 {
		cfg.MaxHTMLBytes = defaultRenderMaxHTMLBytes
	}
	if int64(len(job.HTML)) > cfg.MaxHTMLBytes {
		return renderExportPDFResult{}, fmt.Errorf("render job %s carries %d HTML bytes, above the %d-byte limit (BONFIRE_RENDER_MAX_HTML_BYTES)", job.ID, len(job.HTML), cfg.MaxHTMLBytes)
	}
	chromiumBin, err := resolveRenderBinary("chromium", cfg.ChromiumBin, "RENDER_CHROMIUM_BIN")
	if err != nil {
		return renderExportPDFResult{}, err
	}
	pdftoppmBin, err := resolveRenderBinary("pdftoppm", cfg.PdftoppmBin, "RENDER_PDFTOPPM_BIN")
	if err != nil {
		return renderExportPDFResult{}, err
	}

	workDir, err := os.MkdirTemp("", "bonfire-render-job-*")
	if err != nil {
		return renderExportPDFResult{}, fmt.Errorf("create render work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	// Deck kind delegates pagination to the document's own print CSS; inject the
	// chassis print block as a fallback when the author dropped it, so chromium
	// paginates every slide instead of collapsing to the single on-screen frame
	// (the one-page-deck defect). Paper (text-native) is untouched — it prints
	// direct with no slide model. The CSP meta is pinned last, in front.
	printHTML := job.HTML
	if normalizeRenderJobKind(job.Kind) == renderJobKindDeck {
		printHTML = injectRenderDeckPrintCSS(printHTML)
	}
	printServer, err := startRenderPrintServer(injectRenderPrintCSP(printHTML))
	if err != nil {
		return renderExportPDFResult{}, err
	}
	defer printServer.Close()
	layeredPath := filepath.Join(workDir, "layered.pdf")

	// The flatten law's pinned print arguments (spec §4 14b): --headless=new
	// --no-pdf-header-footer --virtual-time-budget=15000, plus the sandbox
	// flags headless chromium needs inside the container. Chromium keeps its OS
	// sandbox: the process runs as a dedicated non-root user and this invocation
	// deliberately never passes --no-sandbox. The document comes from a fresh
	// loopback HTTP origin, not file://, so scripts cannot read local files or
	// /proc. The response-header CSP sandboxes navigation and the blackholed
	// proxy denies remote schemes underneath the browser policy.
	chromiumArgs := []string{
		"--headless=new",
		// Force the non-SUID namespace sandbox. The container is non-root,
		// drops every capability, and sets no-new-privileges, so a setuid
		// helper is intentionally unavailable.
		"--disable-setuid-sandbox",
		"--disable-gpu",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-default-apps",
		"--disable-extensions",
		"--disable-sync",
		"--metrics-recording-only",
		"--no-first-run",
		"--proxy-server=127.0.0.1:9",
		"--proxy-bypass-list=127.0.0.1",
		"--user-data-dir=" + filepath.Join(workDir, "chrome-profile"),
		"--no-pdf-header-footer",
		"--virtual-time-budget=15000",
		"--print-to-pdf=" + layeredPath,
		printServer.URL,
	}
	if _, stderr, err := runRenderExecCommand(ctx, chromiumBin, chromiumArgs, workDir); err != nil {
		return renderExportPDFResult{}, fmt.Errorf("chromium print-to-pdf failed: %w (stderr: %s)", err, compactAssistantLine(stderr))
	}
	layered, err := os.ReadFile(layeredPath)
	if err != nil {
		return renderExportPDFResult{}, fmt.Errorf("chromium produced no PDF: %w", err)
	}

	// Rasterize every page at renderRasterDPI (144). The JPEGs are the flatten
	// input for decks AND the page images Wave 5's vision slide juries
	// consume, so both kinds run this step.
	pagePrefix := filepath.Join(workDir, "page")
	pdftoppmArgs := []string{"-jpeg", "-r", strconv.Itoa(renderRasterDPI), layeredPath, pagePrefix}
	if _, stderr, err := runRenderExecCommand(ctx, pdftoppmBin, pdftoppmArgs, workDir); err != nil {
		return renderExportPDFResult{}, fmt.Errorf("pdftoppm rasterize failed: %w (stderr: %s)", err, compactAssistantLine(stderr))
	}
	jpegPaths, err := filepath.Glob(pagePrefix + "-*.jpg")
	if err != nil {
		return renderExportPDFResult{}, fmt.Errorf("collect rasterized pages: %w", err)
	}
	sort.Strings(jpegPaths)
	if len(jpegPaths) == 0 {
		return renderExportPDFResult{}, fmt.Errorf("pdftoppm produced no page images")
	}
	jpegPages := make([][]byte, 0, len(jpegPaths))
	for _, path := range jpegPaths {
		page, err := os.ReadFile(path)
		if err != nil {
			return renderExportPDFResult{}, fmt.Errorf("read rasterized page %s: %w", filepath.Base(path), err)
		}
		jpegPages = append(jpegPages, page)
	}

	var pdfBytes []byte
	flattened := false
	switch normalizeRenderJobKind(job.Kind) {
	case renderJobKindPaper:
		// Text-native paper kit: ship chromium's direct print, no flatten —
		// there are no blends to break and text must stay selectable.
		pdfBytes = layered
	default:
		// Deck: the flatten law is non-negotiable — never ship the layered
		// print; the flattened raster is the deliverable, each page sized to
		// its own raster at the shared density.
		assembled, err := assembleJPEGPDF(jpegPages, renderRasterDPI)
		if err != nil {
			return renderExportPDFResult{}, fmt.Errorf("flatten deck pages: %w", err)
		}
		pdfBytes = assembled
		flattened = true
	}
	if cfg.MaxPDFBytes > 0 && int64(len(pdfBytes)) > cfg.MaxPDFBytes {
		return renderExportPDFResult{}, fmt.Errorf("exported PDF is %d bytes, above the %d-byte limit (BONFIRE_RENDER_MAX_PDF_BYTES)", len(pdfBytes), cfg.MaxPDFBytes)
	}

	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		return renderExportPDFResult{}, fmt.Errorf("create render results directory: %w", err)
	}
	pdfPath := filepath.Join(resultsDir, strings.TrimSpace(job.ID)+".pdf")
	if err := os.WriteFile(pdfPath, pdfBytes, 0o644); err != nil {
		return renderExportPDFResult{}, fmt.Errorf("persist exported PDF: %w", err)
	}
	pageRefs := make([]string, 0, len(jpegPaths))
	for index, path := range jpegPaths {
		ref := filepath.Join(resultsDir, filepath.Base(path))
		if err := os.WriteFile(ref, jpegPages[index], 0o644); err != nil {
			return renderExportPDFResult{}, fmt.Errorf("persist page image %s: %w", filepath.Base(path), err)
		}
		pageRefs = append(pageRefs, ref)
	}

	return renderExportPDFResult{
		PDFBytes:      pdfBytes,
		PDFPath:       pdfPath,
		PageJPEGPaths: pageRefs,
		PageCount:     len(pageRefs),
		Flattened:     flattened,
		// A flattened deck (never paper) that produced a single page did not
		// paginate — disclose it downstream.
		DeckSinglePage: flattened && len(pageRefs) == 1,
	}, nil
}

func runRenderExecCommandContext(ctx context.Context, bin string, args []string, dir string) (string, string, error) {
	command := exec.CommandContext(ctx, bin, args...)
	command.Dir = dir
	// Chromium owns a process tree. Killing only the browser parent on timeout
	// leaves its zygote/renderer children behind until the container exhausts
	// its PID budget. Give every render invocation a fresh process group and
	// make context cancellation terminate that entire group.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil {
			if err == syscall.ESRCH {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	command.WaitDelay = 2 * time.Second
	// Do not let Chromium or pdftoppm inherit the callback bearer token, API
	// keys, database URLs, or any other container environment. The allowlist is
	// intentionally static and contains no credentials.
	command.Env = renderSubprocessEnv(dir)
	var stdout cappedBuffer
	var stderr cappedBuffer
	stdout.Limit = defaultCodexExecMaxOutputBytes
	stderr.Limit = defaultCodexExecMaxOutputBytes
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	// The render runner is PID 1 in its purpose-built container. Chromium's
	// sandbox can leave exited zygote grandchildren behind after the direct
	// browser process has been waited. Linux reparents those descendants to PID
	// 1, so reap them without blocking after every serialized invocation. If we
	// do not, two defunct processes per print eventually consume the sidecar's
	// 256-PID cgroup and make every later export fail with EAGAIN.
	reapExitedRenderDescendants(os.Getpid() == 1, syscall.Wait4)
	if ctx.Err() != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("render command timed out or was canceled: %w", ctx.Err())
	}
	return stdout.String(), stderr.String(), err
}

type renderWait4Func func(int, *syscall.WaitStatus, int, *syscall.Rusage) (int, error)

func reapExitedRenderDescendants(pidOne bool, wait4 renderWait4Func) int {
	if !pidOne || wait4 == nil {
		return 0
	}
	reaped := 0
	for {
		pid, err := wait4(-1, nil, syscall.WNOHANG, nil)
		if pid > 0 {
			reaped++
			continue
		}
		if err == syscall.EINTR {
			continue
		}
		return reaped
	}
}

func renderSubprocessEnv(workDir string) []string {
	return []string{
		"HOME=" + filepath.Clean(workDir),
		"TMPDIR=" + filepath.Clean(workDir),
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"TZ=UTC",
	}
}

func renderRunnerCommandEvidence(cfg renderExecConfig, result renderExportPDFResult) string {
	parts := []string{
		"chromium=" + cfg.ChromiumBin,
		"pdftoppm=" + cfg.PdftoppmBin,
		"pages=" + strconv.Itoa(result.PageCount),
		"flattened=" + strconv.FormatBool(result.Flattened),
		"pdf_bytes=" + strconv.Itoa(len(result.PDFBytes)),
	}
	return strings.Join(parts, "\n")
}

func renderRunnerID() string {
	if value := strings.TrimSpace(os.Getenv("BONFIRE_RENDER_RUNNER_ID")); value != "" {
		return value
	}
	hostname, _ := os.Hostname()
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		hostname = "render-runner"
	}
	return hostname + "-" + strconv.Itoa(os.Getpid())
}

func renderRunnerCanaryErrorCode(err error) string {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(message, "timed out") || strings.Contains(message, "deadline"):
		return "timeout"
	case strings.Contains(message, "chromium") && strings.Contains(message, "not found"):
		return "chromium_missing"
	case strings.Contains(message, "pdftoppm") && strings.Contains(message, "not found"):
		return "pdftoppm_missing"
	case strings.Contains(message, "chromium"):
		return "chromium_print_failed"
	case strings.Contains(message, "pdftoppm") || strings.Contains(message, "page image"):
		return "pdf_raster_failed"
	case strings.Contains(message, "pdf"):
		return "pdf_invalid"
	default:
		return "pipeline_failed"
	}
}

// runRenderRunnerCanary uses executeRenderExportPDF rather than a second
// approximation of the toolchain. It therefore exercises the same Chromium
// sandbox arguments, loopback print origin, pdftoppm invocation, output reads,
// and size limits as a real paper export, under the exact running container's
// user/capability/seccomp/filesystem/network policy.
func runRenderRunnerCanary(ctx context.Context, cfg renderExecConfig) (renderRunnerCanaryResult, error) {
	checkedAt := time.Now().UTC()
	timeout := defaultRenderCanaryTimeout
	if cfg.Timeout > 0 && cfg.Timeout < timeout {
		timeout = cfg.Timeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	root, err := os.MkdirTemp("", "bonfire-render-canary-*")
	if err != nil {
		result := renderRunnerCanaryResult{CheckedAt: checkedAt, ErrorCode: "workspace_failed"}
		return result, fmt.Errorf("create render canary workspace: %w", err)
	}
	defer os.RemoveAll(root)
	result, err := executeRenderExportPDF(runCtx, cfg, renderRunnerJob{
		ID:         "render-canary",
		Type:       renderJobTypeExportPDF,
		ArtifactID: "render-canary",
		Kind:       renderJobKindPaper,
		HTML:       "<!doctype html><html><head><meta charset=\"utf-8\"><title>Render canary</title></head><body><main>STRIDE render canary</main></body></html>",
	}, filepath.Join(root, "results"))
	if err != nil {
		canary := renderRunnerCanaryResult{CheckedAt: checkedAt, ErrorCode: renderRunnerCanaryErrorCode(err)}
		return canary, err
	}
	canary := renderRunnerCanaryResult{CheckedAt: checkedAt, PageCount: result.PageCount, PDFBytes: len(result.PDFBytes)}
	if result.PageCount != 1 || len(result.PageJPEGPaths) != 1 || len(result.PDFBytes) < 5 || !bytes.Equal(result.PDFBytes[:5], []byte("%PDF-")) {
		canary.ErrorCode = "pdf_invalid"
		return canary, fmt.Errorf("render canary produced invalid PDF/page outputs")
	}
	page, err := os.ReadFile(result.PageJPEGPaths[0])
	if err != nil || len(page) < 3 || page[0] != 0xff || page[1] != 0xd8 || page[2] != 0xff {
		canary.ErrorCode = "pdf_raster_failed"
		return canary, fmt.Errorf("render canary produced no valid JPEG page")
	}
	canary.OK = true
	return canary, nil
}

func writeRenderRunnerHeartbeat(runnerID string, canary renderRunnerCanaryResult) error {
	cfg := renderExecConfigFromEnv()
	_, chromiumErr := resolveRenderBinary("chromium", cfg.ChromiumBin, "RENDER_CHROMIUM_BIN")
	_, pdftoppmErr := resolveRenderBinary("pdftoppm", cfg.PdftoppmBin, "RENDER_PDFTOPPM_BIN")
	toolchainOK := chromiumErr == nil && pdftoppmErr == nil
	canaryOK := canary.OK && !canary.CheckedAt.IsZero() && canary.PageCount == 1 && canary.PDFBytes >= 5
	if !toolchainOK && canary.ErrorCode == "" {
		if chromiumErr != nil {
			canary.ErrorCode = "chromium_missing"
		} else {
			canary.ErrorCode = "pdftoppm_missing"
		}
	}
	payload := map[string]any{
		"ok":              toolchainOK && canaryOK,
		"runnerId":        runnerID,
		"queuePath":       renderRunnerQueuePath(),
		"chromiumOK":      chromiumErr == nil,
		"pdftoppmOK":      pdftoppmErr == nil,
		"canaryOK":        canaryOK,
		"canaryCheckedAt": canary.CheckedAt.Format(time.RFC3339Nano),
		"canaryPageCount": canary.PageCount,
		"canaryPDFBytes":  canary.PDFBytes,
		"canaryErrorCode": canary.ErrorCode,
		"time":            time.Now().UTC().Format(time.RFC3339Nano),
	}
	return writeJSONFileAtomically(renderRunnerHeartbeatPath(), "render runner heartbeat", payload)
}

// readinessRenderRunnerSnapshot mirrors readinessCodexRunnerSnapshot for the
// stage-B readiness wiring: a missing or stale heartbeat is exactly the
// "render sidecar not available" signal the OS surfaces to users.
func readinessRenderRunnerSnapshot() map[string]any {
	snapshot := map[string]any{
		"queuePath":       renderRunnerQueuePath(),
		"heartbeatPath":   renderRunnerHeartbeatPath(),
		"callbackSecured": strings.TrimSpace(os.Getenv("BONFIRE_RUNNER_TOKEN")) != "",
	}
	raw, err := os.ReadFile(renderRunnerHeartbeatPath())
	if err != nil {
		snapshot["heartbeatOK"] = false
		snapshot["heartbeatError"] = "missing"
		return snapshot
	}
	var heartbeat struct {
		OK              bool   `json:"ok"`
		RunnerID        string `json:"runnerId"`
		ChromiumOK      bool   `json:"chromiumOK"`
		PdftoppmOK      bool   `json:"pdftoppmOK"`
		CanaryOK        bool   `json:"canaryOK"`
		CanaryCheckedAt string `json:"canaryCheckedAt"`
		CanaryPageCount int    `json:"canaryPageCount"`
		CanaryErrorCode string `json:"canaryErrorCode"`
		Time            string `json:"time"`
	}
	if err := json.Unmarshal(raw, &heartbeat); err != nil {
		snapshot["heartbeatOK"] = false
		snapshot["heartbeatError"] = "invalid"
		return snapshot
	}
	parsed, err := time.Parse(time.RFC3339Nano, heartbeat.Time)
	if err != nil {
		snapshot["heartbeatOK"] = false
		snapshot["heartbeatError"] = "invalid_time"
		return snapshot
	}
	canaryCheckedAt, canaryErr := time.Parse(time.RFC3339Nano, heartbeat.CanaryCheckedAt)
	if canaryErr != nil {
		snapshot["heartbeatOK"] = false
		snapshot["heartbeatError"] = "invalid_canary_time"
		return snapshot
	}
	age := time.Since(parsed)
	canaryAge := time.Since(canaryCheckedAt)
	const allowedFutureSkew = 5 * time.Second
	snapshot["heartbeatOK"] = age >= -allowedFutureSkew && age <= defaultRenderRunnerStaleAfter &&
		canaryAge >= -allowedFutureSkew && canaryAge <= defaultRenderRunnerStaleAfter &&
		heartbeat.OK && heartbeat.ChromiumOK && heartbeat.PdftoppmOK && heartbeat.CanaryOK && heartbeat.CanaryPageCount == 1
	snapshot["heartbeatAgeSeconds"] = int(age.Seconds())
	snapshot["canaryAgeSeconds"] = int(canaryAge.Seconds())
	snapshot["runnerId"] = heartbeat.RunnerID
	snapshot["chromiumOK"] = heartbeat.ChromiumOK
	snapshot["pdftoppmOK"] = heartbeat.PdftoppmOK
	snapshot["canaryOK"] = heartbeat.CanaryOK
	snapshot["canaryPageCount"] = heartbeat.CanaryPageCount
	snapshot["canaryErrorCode"] = heartbeat.CanaryErrorCode
	return snapshot
}

func sendRenderRunnerCallback(ctx context.Context, payload renderRunnerCallbackPayload) error {
	callbackURL := strings.TrimSpace(os.Getenv("BONFIRE_RENDER_CALLBACK_URL"))
	if callbackURL == "" {
		callbackURL = "http://meetingassist:3000/internal/render/jobs/result"
	}
	token := strings.TrimSpace(os.Getenv("BONFIRE_RUNNER_TOKEN"))
	if token == "" {
		return fmt.Errorf("BONFIRE_RUNNER_TOKEN is required for render runner callbacks")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode render runner callback: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("create render runner callback request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send render runner callback: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("render runner callback returned %s", resp.Status)
	}
	return nil
}
