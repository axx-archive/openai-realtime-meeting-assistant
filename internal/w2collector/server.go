package w2collector

import (
	"bytes"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/w2driver"
	"github.com/openai/openai-realtime-meeting-assistant/internal/w2gate"
)

const (
	GateBundleSchema  = "bonfire.w2.collector.gate-bundle.v1"
	ProbeBundleSchema = "bonfire.w2.collector.probe-bundle.v1"
	maxRequestBytes   = 1 << 20
)

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9._:+-]{2,127}$`)

// Server is the checked collector boundary. It serves retained, body-free raw
// evidence bundles, binds them to a one-use caller challenge, and signs the
// complete response with the independently custodied collector key.
type Server struct {
	ReleaseCommit                 string
	CollectorImplementationDigest string
	KeyID                         string
	PrivateKey                    ed25519.PrivateKey
	Token                         string
	BundleDir                     string
	Now                           func() time.Time

	mu         sync.Mutex
	usedNonces map[string]struct{}
}

type GateArtifactManifest struct {
	Schema        string              `json:"schema"`
	GateID        string              `json:"gateId"`
	ReleaseCommit string              `json:"releaseCommit"`
	CorpusDigest  string              `json:"corpusDigest"`
	Artifacts     []ArtifactReference `json:"artifacts"`
}

type ProbeArtifactManifest struct {
	Schema        string              `json:"schema"`
	ProbeID       string              `json:"probeId"`
	ReleaseCommit string              `json:"releaseCommit"`
	Artifacts     []ArtifactReference `json:"artifacts"`
}

type ArtifactReference struct {
	Kind             string `json:"kind"`
	SnapshotID       string `json:"snapshotId,omitempty"`
	CorpusItemDigest string `json:"corpusItemDigest,omitempty"`
	Name             string `json:"name"`
	Path             string `json:"path"`
	SHA256           string `json:"sha256"`
}

func (server *Server) Validate() error {
	if w2gate.ValidateReleaseCommit(server.ReleaseCommit) != nil || !strongDigest(server.CollectorImplementationDigest) ||
		strings.TrimSpace(server.KeyID) == "" || len(server.PrivateKey) != ed25519.PrivateKeySize || len(strings.TrimSpace(server.Token)) < 16 || strings.TrimSpace(server.BundleDir) == "" {
		return errors.New("collector server configuration is incomplete")
	}
	return nil
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if err := server.Validate(); err != nil {
		http.Error(writer, "collector unavailable", http.StatusServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost || !server.authorized(request.Header.Get("Authorization")) {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/")
	switch {
	case strings.HasPrefix(path, "v1/w2/gates/"):
		server.serveGate(writer, request, strings.TrimPrefix(path, "v1/w2/gates/"))
	case strings.HasPrefix(path, "v1/w2/probes/"):
		server.serveProbe(writer, request, strings.TrimPrefix(path, "v1/w2/probes/"))
	default:
		http.NotFound(writer, request)
	}
}

func (server *Server) serveGate(writer http.ResponseWriter, request *http.Request, gateID string) {
	if !identifier.MatchString(gateID) {
		http.Error(writer, "invalid gate", http.StatusBadRequest)
		return
	}
	var incoming w2driver.CollectorRequest
	if err := decode(request.Body, &incoming); err != nil || incoming.Schema != w2driver.CollectorRequestSchema || incoming.GateID != gateID ||
		incoming.ReleaseCommit != server.ReleaseCommit || incoming.CollectorImplementationDigest != server.CollectorImplementationDigest || !strongDigest(incoming.ChallengeNonce) {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	if !server.claimNonce(incoming.ChallengeNonce) {
		http.Error(writer, "challenge already used", http.StatusConflict)
		return
	}
	var bundle GateArtifactManifest
	if err := server.loadBundle("gate-"+gateID+".json", &bundle); err != nil || bundle.Schema != GateBundleSchema || bundle.GateID != gateID || bundle.ReleaseCommit != incoming.ReleaseCommit || bundle.CorpusDigest != incoming.CorpusDigest {
		http.Error(writer, "gate evidence unavailable", http.StatusPreconditionFailed)
		return
	}
	artifacts, err := server.loadArtifacts(bundle.Artifacts)
	if err != nil {
		http.Error(writer, "gate raw artifacts unavailable", http.StatusPreconditionFailed)
		return
	}
	snapshots, err := w2driver.ReplayGateArtifacts(artifacts)
	if err != nil {
		http.Error(writer, "gate evidence invalid", http.StatusPreconditionFailed)
		return
	}
	manifestDigest, err := w2driver.RawArtifactManifestDigest(artifacts)
	if err != nil {
		http.Error(writer, "gate evidence invalid", http.StatusPreconditionFailed)
		return
	}
	now := server.now()
	response := w2driver.CollectorResponse{
		Schema: w2driver.CollectorResponseSchema, GateID: gateID, ReleaseCommit: incoming.ReleaseCommit, CorpusDigest: incoming.CorpusDigest,
		ChallengeNonce: incoming.ChallengeNonce, CollectorImplementationDigest: server.CollectorImplementationDigest,
		RawArtifactManifestDigest: manifestDigest, ObservedAt: now, ExpiresAt: now.Add(10 * time.Minute), Snapshots: snapshots, Artifacts: artifacts,
	}
	if err := response.Sign(server.KeyID, server.PrivateKey); err != nil {
		http.Error(writer, "collector signing failed", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, response)
}

func (server *Server) serveProbe(writer http.ResponseWriter, request *http.Request, probeID string) {
	if !identifier.MatchString(probeID) {
		http.Error(writer, "invalid probe", http.StatusBadRequest)
		return
	}
	var incoming w2driver.ProbeRequest
	if err := decode(request.Body, &incoming); err != nil || incoming.Schema != "bonfire.w2.system-probe.request.v2" || incoming.ProbeID != probeID ||
		incoming.ReleaseCommit != server.ReleaseCommit || incoming.CollectorImplementationDigest != server.CollectorImplementationDigest || !strongDigest(incoming.ChallengeNonce) {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	if !server.claimNonce(incoming.ChallengeNonce) {
		http.Error(writer, "challenge already used", http.StatusConflict)
		return
	}
	var bundle ProbeArtifactManifest
	if err := server.loadBundle("probe-"+safeFilename(probeID)+".json", &bundle); err != nil || bundle.Schema != ProbeBundleSchema || bundle.ProbeID != probeID || bundle.ReleaseCommit != incoming.ReleaseCommit || len(bundle.Artifacts) == 0 {
		http.Error(writer, "probe evidence unavailable", http.StatusPreconditionFailed)
		return
	}
	artifacts, err := server.loadArtifacts(bundle.Artifacts)
	if err != nil {
		http.Error(writer, "probe raw artifacts unavailable", http.StatusPreconditionFailed)
		return
	}
	assertions, err := w2driver.ReplayProbeArtifacts(artifacts)
	if err != nil {
		http.Error(writer, "probe evidence invalid", http.StatusPreconditionFailed)
		return
	}
	manifestDigest, err := w2driver.ProbeArtifactManifestDigest(artifacts)
	if err != nil {
		http.Error(writer, "probe evidence invalid", http.StatusPreconditionFailed)
		return
	}
	now := server.now()
	response := w2driver.SystemProbeResponse{
		Schema: w2driver.SystemProbeSchema, ProbeID: probeID, ReleaseCommit: incoming.ReleaseCommit,
		ChallengeNonce: incoming.ChallengeNonce, CollectorImplementationDigest: server.CollectorImplementationDigest,
		RawArtifactManifestDigest: manifestDigest, ObservedAt: now, ExpiresAt: now.Add(10 * time.Minute), Assertions: assertions, Artifacts: artifacts,
	}
	if err := response.Sign(server.KeyID, server.PrivateKey); err != nil {
		http.Error(writer, "collector signing failed", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, response)
}

func (server *Server) authorized(header string) bool {
	want := []byte("Bearer " + server.Token)
	actual := []byte(header)
	return len(actual) == len(want) && subtle.ConstantTimeCompare(actual, want) == 1
}

func (server *Server) claimNonce(nonce string) bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.usedNonces == nil {
		server.usedNonces = map[string]struct{}{}
	}
	if _, exists := server.usedNonces[nonce]; exists {
		return false
	}
	server.usedNonces[nonce] = struct{}{}
	return true
}

func (server *Server) loadBundle(name string, target any) error {
	if filepath.Base(name) != name {
		return errors.New("invalid bundle name")
	}
	raw, err := os.ReadFile(filepath.Join(server.BundleDir, name))
	if err != nil {
		return err
	}
	return decode(bytes.NewReader(raw), target)
}

func (server *Server) loadArtifacts(references []ArtifactReference) ([]w2driver.RawArtifact, error) {
	if len(references) == 0 {
		return nil, errors.New("artifact manifest is empty")
	}
	result := make([]w2driver.RawArtifact, 0, len(references))
	seen := map[string]bool{}
	for _, reference := range references {
		if filepath.Base(reference.Path) != reference.Path || reference.Path == "" || seen[reference.Name] || !strongDigest(reference.SHA256) {
			return nil, errors.New("artifact reference is invalid")
		}
		seen[reference.Name] = true
		path := filepath.Join(server.BundleDir, reference.Path)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxRequestBytes {
			return nil, errors.New("artifact file is unavailable")
		}
		raw, err := os.ReadFile(path)
		if err != nil || w2gate.DigestBytes(raw) != reference.SHA256 {
			return nil, errors.New("artifact digest mismatch")
		}
		artifact := w2driver.RawArtifact{Kind: reference.Kind, SnapshotID: reference.SnapshotID, CorpusItemDigest: reference.CorpusItemDigest, Name: reference.Name, SHA256: reference.SHA256, Payload: json.RawMessage(raw)}
		if artifact.Validate() != nil {
			return nil, errors.New("artifact payload is invalid")
		}
		result = append(result, artifact)
	}
	return result, nil
}

func (server *Server) now() time.Time {
	if server.Now != nil {
		return server.Now().UTC()
	}
	return time.Now().UTC()
}

func decode(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maxRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		panic(fmt.Sprintf("encode collector response: %v", err))
	}
}

func safeFilename(value string) string {
	return strings.NewReplacer(":", "-", "/", "-").Replace(value)
}

func strongDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != 32 {
		return false
	}
	for index := 1; index < len(raw); index++ {
		if raw[index] != raw[0] {
			return true
		}
	}
	return false
}
