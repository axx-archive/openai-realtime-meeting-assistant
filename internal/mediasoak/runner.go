package mediasoak

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

type Probe interface {
	Observe(context.Context, Manifest, string) (SignedObservation, error)
}

type FileProbe struct{ Path string }

func (probe FileProbe) Observe(ctx context.Context, _ Manifest, _ string) (SignedObservation, error) {
	select {
	case <-ctx.Done():
		return SignedObservation{}, ctx.Err()
	default:
	}
	raw, err := os.ReadFile(probe.Path)
	if err != nil {
		return SignedObservation{}, err
	}
	return DecodeSignedObservation(raw)
}

// CommandProbe runs the checked, same-commit collector command and then has the
// release operator sign only the aggregate that this package recomputes from
// the collector-signed raw evidence. The release private key is never passed
// in the collector's environment or command line.
type CommandProbe struct {
	Root              string
	ReleaseKeyID      string
	ReleasePrivateKey ed25519.PrivateKey
	Environment       []string
	verifyCheckout    func(context.Context, string, string) (ExecutableAttestation, error)
	runCollector      func(context.Context, string, []string, []string, string) error
}

func (probe CommandProbe) Observe(ctx context.Context, manifest Manifest, releaseCommit string) (SignedObservation, error) {
	if len(probe.ReleasePrivateKey) != ed25519.PrivateKeySize || strings.TrimSpace(probe.ReleaseKeyID) == "" {
		return SignedObservation{}, ErrInvalidSignature
	}
	if !contains(manifest.AllowedSignerKeyIDs, probe.ReleaseKeyID) {
		return SignedObservation{}, ErrInvalidSignature
	}
	checkoutVerifier := verifyCollectorCheckout
	if probe.verifyCheckout != nil {
		checkoutVerifier = probe.verifyCheckout
	}
	executable, err := checkoutVerifier(ctx, probe.Root, releaseCommit)
	if err != nil {
		return SignedObservation{}, fmt.Errorf("%w: collector checkout identity: %v", ErrInconclusive, err)
	}
	outputRelative := strings.ReplaceAll(manifest.Collector.SignedEvidencePath, "{releaseCommit}", releaseCommit)
	outputPath, err := rootedPath(probe.Root, outputRelative)
	if err != nil {
		return SignedObservation{}, err
	}
	if _, err := os.Lstat(outputPath); err == nil {
		return SignedObservation{}, fmt.Errorf("%w: collector output already exists", ErrInconclusive)
	} else if !errors.Is(err, os.ErrNotExist) {
		return SignedObservation{}, err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return SignedObservation{}, err
	}
	arguments := make([]string, 0, len(manifest.Collector.Command)-1)
	for _, argument := range manifest.Collector.Command[1:] {
		argument = strings.ReplaceAll(argument, "{releaseCommit}", releaseCommit)
		argument = strings.ReplaceAll(argument, "{signedEvidencePath}", outputPath)
		arguments = append(arguments, argument)
	}
	environment := collectorEnvironment(os.Environ(), probe.Environment, releaseCommit, outputPath)
	runCollector := func(ctx context.Context, commandName string, arguments, environment []string, root string) error {
		command := exec.CommandContext(ctx, commandName, arguments...)
		command.Dir = root
		command.Env = environment
		command.Stdout = os.Stderr
		command.Stderr = os.Stderr
		return command.Run()
	}
	collectorRoot := probe.Root
	var removeExport func()
	if probe.runCollector != nil {
		runCollector = probe.runCollector
	} else {
		collectorRoot, removeExport, err = exportCollectorSource(ctx, probe.Root, releaseCommit, executable)
		if err != nil {
			return SignedObservation{}, fmt.Errorf("%w: immutable collector export: %v", ErrInconclusive, err)
		}
		defer removeExport()
		environment = append(environment, "BONFIRE_MEDIA_SOAK_EVIDENCE_ROOT="+probe.Root)
	}
	if err := runCollector(ctx, manifest.Collector.Command[0], arguments, environment, collectorRoot); err != nil {
		if ctx.Err() != nil {
			return SignedObservation{}, fmt.Errorf("%w: collector interrupted: %v", ErrInconclusive, ctx.Err())
		}
		return SignedObservation{}, fmt.Errorf("%w: collector failed: %v", ErrInconclusive, err)
	}
	raw, err := os.ReadFile(outputPath)
	if err != nil {
		return SignedObservation{}, fmt.Errorf("%w: collector did not emit signed raw evidence: %v", ErrInconclusive, err)
	}
	var evidence SignedRawEvidence
	if err := decodeStrict(raw, &evidence); err != nil {
		return SignedObservation{}, fmt.Errorf("%w: malformed collector evidence: %v", ErrInvalidObservation, err)
	}
	if !reflect.DeepEqual(evidence.Payload.Executable, executable) {
		return SignedObservation{}, fmt.Errorf("%w: collector evidence does not match independently checked executable identity", ErrInvalidObservation)
	}
	releasePublicKey := probe.ReleasePrivateKey.Public().(ed25519.PublicKey)
	trustedRelease, trusted := manifest.trustedSigner(SignerRoleRelease, probe.ReleaseKeyID)
	if !trusted || digest(releasePublicKey) != trustedRelease.PublicKeySHA256 {
		return SignedObservation{}, ErrInvalidSignature
	}
	if err := verifyArtifactEvidence(probe.Root, evidence.Payload, manifest, releasePublicKey); err != nil {
		return SignedObservation{}, err
	}
	observation, err := RecomputeObservation(manifest, evidence.Payload)
	if err != nil {
		return SignedObservation{}, err
	}
	return SignObservationWithEvidence(observation, evidence, probe.ReleaseKeyID, probe.ReleasePrivateKey)
}

type collectorSourceInput struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type collectorSourceManifest struct {
	Schema     string                 `json:"schema"`
	Executable ExecutableAttestation  `json:"executable"`
	Inputs     []collectorSourceInput `json:"inputs"`
}

func exportCollectorSource(ctx context.Context, root, releaseCommit string, executable ExecutableAttestation) (string, func(), error) {
	command := exec.CommandContext(ctx, "git", "archive", "--format=tar", releaseCommit)
	command.Dir = root
	archive, err := command.Output()
	if err != nil || len(archive) == 0 || digest(archive) != executable.SourceArchiveSHA256 {
		return "", nil, errors.New("git archive does not match checked source digest")
	}
	temporary, err := os.MkdirTemp("", "bonfire-media-soak-source-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = filepath.Walk(temporary, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr == nil && info.IsDir() {
				_ = os.Chmod(path, 0o755)
			}
			return nil
		})
		_ = os.RemoveAll(temporary)
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	inputs := make([]collectorSourceInput, 0, executable.InputCount)
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			cleanup()
			return "", nil, nextErr
		}
		entryName := strings.TrimSuffix(header.Name, "/")
		if validateRelativePath(entryName) != nil {
			cleanup()
			return "", nil, errors.New("git archive contains an unsafe path")
		}
		path := filepath.Join(temporary, filepath.FromSlash(entryName))
		switch header.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				cleanup()
				return "", nil, err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				cleanup()
				return "", nil, err
			}
			body, readErr := io.ReadAll(io.LimitReader(reader, header.Size+1))
			if readErr != nil || int64(len(body)) != header.Size {
				cleanup()
				return "", nil, errors.New("git archive entry is truncated")
			}
			if err := os.WriteFile(path, body, 0o444); err != nil {
				cleanup()
				return "", nil, err
			}
			inputs = append(inputs, collectorSourceInput{Path: filepath.ToSlash(entryName), SHA256: digest(body)})
		default:
			cleanup()
			return "", nil, errors.New("git archive contains a non-regular input")
		}
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Path < inputs[j].Path })
	if len(inputs) != executable.InputCount {
		cleanup()
		return "", nil, errors.New("git archive input count differs from transitive inventory")
	}
	manifestRaw, err := json.Marshal(collectorSourceManifest{Schema: "bonfire.w2a.immutable-source-export.v1", Executable: executable, Inputs: inputs})
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if err := os.WriteFile(filepath.Join(temporary, ".bonfire-release-source.json"), append(manifestRaw, '\n'), 0o444); err != nil {
		cleanup()
		return "", nil, err
	}
	_ = filepath.Walk(temporary, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr == nil && info.IsDir() {
			return os.Chmod(path, 0o555)
		}
		return walkErr
	})
	return temporary, cleanup, nil
}

func verifyCollectorCheckout(ctx context.Context, root, releaseCommit string) (ExecutableAttestation, error) {
	gitOutput := func(arguments ...string) (string, error) {
		command := exec.CommandContext(ctx, "git", arguments...)
		command.Dir = root
		output, err := command.Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(output)), nil
	}
	head, err := gitOutput("rev-parse", "HEAD")
	if err != nil || head != releaseCommit {
		return ExecutableAttestation{}, errors.New("checkout HEAD is not the requested release")
	}
	paths := []string{"testdata/w2a/live-media-soak-probe.mjs", "scripts/live-media-smoke.mjs", "cmd/media-soak/main.go"}
	dirty, err := gitOutput("status", "--porcelain", "--untracked-files=all")
	if err != nil || dirty != "" {
		return ExecutableAttestation{}, errors.New("collector executable inputs are modified")
	}
	blobs := make([]string, 0, 3)
	for _, path := range paths {
		working, workingErr := gitOutput("hash-object", path)
		committed, committedErr := gitOutput("rev-parse", releaseCommit+":"+path)
		if workingErr != nil || committedErr != nil || working != committed || !isGitObjectID(working) {
			return ExecutableAttestation{}, fmt.Errorf("collector executable blob drift: %s", path)
		}
		blobs = append(blobs, working)
	}
	tree, err := gitOutput("rev-parse", releaseCommit+"^{tree}")
	if err != nil || !isGitObjectID(tree) {
		return ExecutableAttestation{}, errors.New("release tree identity is unavailable")
	}
	lsCommand := exec.CommandContext(ctx, "git", "ls-tree", "-r", "--full-tree", releaseCommit)
	lsCommand.Dir = root
	transitive, err := lsCommand.Output()
	if err != nil {
		return ExecutableAttestation{}, errors.New("transitive release inputs are unavailable")
	}
	inputCount := 0
	configLines := make([]string, 0, 5)
	configPaths := map[string]bool{"Dockerfile": true, "go.mod": true, "go.sum": true, "deploy/digitalocean/docker-compose.yml": true, "deploy/digitalocean/Caddyfile": true}
	for _, line := range strings.Split(strings.TrimSuffix(string(transitive), "\n"), "\n") {
		if line == "" {
			continue
		}
		inputCount++
		if fields := strings.SplitN(line, "\t", 2); len(fields) == 2 && configPaths[fields[1]] {
			configLines = append(configLines, line)
		}
	}
	if inputCount == 0 || len(configLines) != len(configPaths) {
		return ExecutableAttestation{}, errors.New("release build configuration inputs are incomplete")
	}
	archiveCommand := exec.CommandContext(ctx, "git", "archive", "--format=tar", releaseCommit)
	archiveCommand.Dir = root
	archive, err := archiveCommand.Output()
	if err != nil || len(archive) == 0 {
		return ExecutableAttestation{}, errors.New("release source archive is unavailable")
	}
	return ExecutableAttestation{
		Head: releaseCommit, GitTreeObject: tree, GitTreeDigest: digest([]byte(tree)), SourceArchiveSHA256: digest(archive),
		TransitiveInputsSHA256: digest(transitive), ConfigSHA256: digest([]byte(strings.Join(configLines, "\n") + "\n")), InputCount: inputCount,
		CollectorGitBlob: blobs[0], BrowserGitBlob: blobs[1], GateGitBlob: blobs[2],
	}, nil
}

func collectorEnvironment(base, additional []string, releaseCommit, outputPath string) []string {
	result := make([]string, 0, len(base)+len(additional)+2)
	for _, entry := range append(append([]string(nil), base...), additional...) {
		name := entry
		if index := strings.IndexByte(name, '='); index >= 0 {
			name = name[:index]
		}
		if name == "BONFIRE_MEDIA_SOAK_RELEASE_PRIVATE_KEY_FILE" || name == "BONFIRE_RELEASE_OPERATOR_PRIVATE_KEY" ||
			name == "BONFIRE_RELEASE_COMMIT" || name == "BONFIRE_MEDIA_SOAK_SIGNED_EVIDENCE_PATH" {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "BONFIRE_RELEASE_COMMIT="+releaseCommit, "BONFIRE_MEDIA_SOAK_SIGNED_EVIDENCE_PATH="+outputPath)
}

func loadArtifactFiles(root string, artifacts []RawEvidenceArtifact) (map[string][]byte, error) {
	contents := make(map[string][]byte, len(artifacts))
	for _, artifact := range artifacts {
		path, err := rootedPath(root, artifact.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: artifact path: %v", ErrInvalidObservation, err)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != artifact.Bytes {
			return nil, fmt.Errorf("%w: artifact is missing, mutable, or has the wrong size", ErrInvalidObservation)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("%w: artifact open: %v", ErrInvalidObservation, err)
		}
		hasher := sha256.New()
		body, readErr := io.ReadAll(io.TeeReader(io.LimitReader(file, artifact.Bytes+1), hasher))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || int64(len(body)) != artifact.Bytes || fmt.Sprintf("%x", hasher.Sum(nil)) != artifact.SHA256 {
			return nil, fmt.Errorf("%w: artifact digest mismatch", ErrInvalidObservation)
		}
		if _, duplicate := contents[artifact.Kind]; duplicate {
			return nil, fmt.Errorf("%w: duplicate artifact kind", ErrInvalidObservation)
		}
		contents[artifact.Kind] = body
	}
	return contents, nil
}

func verifyArtifactEvidence(root string, raw RawEvidenceManifest, manifest Manifest, releasePublicKey ed25519.PublicKey) error {
	contents, err := loadArtifactFiles(root, raw.Artifacts)
	if err != nil {
		return err
	}
	decodeEqual := func(kind string, expected any, actual any) error {
		body, ok := contents[kind]
		if !ok {
			return fmt.Errorf("%w: missing artifact kind %s", ErrInconclusive, kind)
		}
		if err := decodeStrict(body, actual); err != nil || !reflect.DeepEqual(reflect.ValueOf(actual).Elem().Interface(), expected) {
			return fmt.Errorf("%w: artifact %s is not semantically bound to signed raw evidence", ErrInvalidObservation, kind)
		}
		return nil
	}
	var network []RawNetworkSample
	if err := decodeEqual("webrtc_stats", raw.NetworkSamples, &network); err != nil {
		return err
	}
	var resources []RawResourceSample
	if err := decodeEqual("container_metrics", raw.ResourceSamples, &resources); err != nil {
		return err
	}
	var host HostAttestation
	if err := decodeEqual("config_attestation", raw.Host, &host); err != nil {
		return err
	}
	var browser struct {
		RoomSamples []RawRoomSample `json:"roomSamples"`
		Rooms       [][]struct {
			At               time.Time `json:"at"`
			SourceDigest     string    `json:"sourceDigest"`
			ConnectedClients int       `json:"connectedClients"`
			Publishers       int       `json:"publishers"`
			Participants     int       `json:"participants"`
		} `json:"rooms"`
	}
	if err := decodeStrict(contents["browser_events"], &browser); err != nil || !reflect.DeepEqual(browser.RoomSamples, raw.RoomSamples) || len(browser.Rooms) != 2 {
		return fmt.Errorf("%w: artifact browser_events is not semantically bound to signed raw evidence", ErrInvalidObservation)
	}
	roomSamplesBySource := make(map[string]RawRoomSample, len(raw.RoomSamples))
	for _, sample := range raw.RoomSamples {
		if _, duplicate := roomSamplesBySource[sample.SourceDigest]; duplicate {
			return fmt.Errorf("%w: browser sample source digest is not unique", ErrInvalidObservation)
		}
		roomSamplesBySource[sample.SourceDigest] = sample
	}
	seenBrowserSources := make(map[string]bool, len(raw.RoomSamples))
	for _, room := range browser.Rooms {
		for _, event := range room {
			sample, ok := roomSamplesBySource[event.SourceDigest]
			if !ok || seenBrowserSources[event.SourceDigest] || !event.At.Equal(sample.At) || event.ConnectedClients != sample.Subscribers ||
				event.Publishers != sample.Publishers || event.Participants != sample.Participants {
				return fmt.Errorf("%w: retained browser event does not derive its signed room sample", ErrInvalidObservation)
			}
			seenBrowserSources[event.SourceDigest] = true
		}
	}
	if len(seenBrowserSources) != len(raw.RoomSamples) {
		return fmt.Errorf("%w: retained browser events do not cover signed room samples", ErrInvalidObservation)
	}
	var fault struct {
		HeadOfLine   RawHeadOfLineEvidence `json:"headOfLine"`
		AIFailure    RawAIFailureEvidence  `json:"aiFailure"`
		CanaryChecks []RawCanaryCheck      `json:"canaryChecks"`
	}
	if err := decodeStrict(contents["fault_log"], &fault); err != nil || len(raw.AIFailures) != 1 ||
		!reflect.DeepEqual(fault.HeadOfLine, raw.HeadOfLine) || !reflect.DeepEqual(fault.AIFailure, raw.AIFailures[0]) ||
		!reflect.DeepEqual(fault.CanaryChecks, raw.CanaryChecks) {
		return fmt.Errorf("%w: artifact fault_log is not semantically bound to signed raw evidence", ErrInvalidObservation)
	}
	var image struct {
		ImageDigest     string `json:"imageDigest"`
		ContainerDigest string `json:"containerDigest"`
	}
	if err := decodeStrict(contents["probe_image"], &image); err != nil || image.ImageDigest != raw.Host.ImageDigest || image.ContainerDigest != raw.Host.ContainerDigest {
		return fmt.Errorf("%w: artifact probe_image is not semantically bound to signed raw evidence", ErrInvalidObservation)
	}
	var buildManifest struct {
		Schema  string `json:"schema"`
		Payload struct {
			Schema                 string    `json:"schema"`
			ReleaseCommit          string    `json:"releaseCommit"`
			GitTreeObject          string    `json:"gitTreeObject"`
			GitTreeDigest          string    `json:"gitTreeDigest"`
			SourceArchiveSHA256    string    `json:"sourceArchiveSha256"`
			TransitiveInputsSHA256 string    `json:"transitiveInputsSha256"`
			ConfigSHA256           string    `json:"configSha256"`
			InputCount             int       `json:"inputCount"`
			BinarySHA256           string    `json:"binarySha256"`
			OCIImageDigest         string    `json:"ociImageDigest"`
			ImageReference         string    `json:"imageReference"`
			CreatedAt              time.Time `json:"createdAt"`
		} `json:"payload"`
		Signature SignatureEnvelope `json:"signature"`
	}
	if err := decodeStrict(contents["build_manifest"], &buildManifest); err != nil || buildManifest.Schema != "bonfire.w2a.signed-build-manifest.v1" ||
		buildManifest.Payload.Schema != "bonfire.w2a.build-manifest.v1" || buildManifest.Payload.ReleaseCommit != raw.ReleaseCommit ||
		buildManifest.Payload.GitTreeObject != raw.Executable.GitTreeObject || strings.TrimPrefix(buildManifest.Payload.GitTreeDigest, "sha256:") != raw.Executable.GitTreeDigest ||
		strings.TrimPrefix(buildManifest.Payload.SourceArchiveSHA256, "sha256:") != raw.Executable.SourceArchiveSHA256 ||
		strings.TrimPrefix(buildManifest.Payload.TransitiveInputsSHA256, "sha256:") != raw.Executable.TransitiveInputsSHA256 ||
		strings.TrimPrefix(buildManifest.Payload.ConfigSHA256, "sha256:") != raw.Executable.ConfigSHA256 || buildManifest.Payload.InputCount != raw.Executable.InputCount ||
		strings.TrimPrefix(buildManifest.Payload.BinarySHA256, "sha256:") != raw.Host.ContainerDigest || strings.TrimPrefix(buildManifest.Payload.OCIImageDigest, "sha256:") != raw.Host.ImageDigest ||
		buildManifest.Signature.Algorithm != SignatureAlgorithmEd25519 || buildManifest.Signature.KeyID != "bonfire-release-operator" ||
		digest(contents["build_manifest"]) != raw.Host.BuildManifestSHA256 {
		return fmt.Errorf("%w: artifact build_manifest is not bound to release, binary, OCI, config, and transitive inputs", ErrInvalidObservation)
	}
	if err := verifyBuildManifestSignature(manifest, releasePublicKey, buildManifest.Signature, contents["build_manifest"]); err != nil {
		return err
	}
	var probeLog struct {
		CollectorVersion      string                `json:"collectorVersion"`
		ReleaseCommit         string                `json:"releaseCommit"`
		Executable            ExecutableAttestation `json:"executable"`
		RoomCount             int                   `json:"roomCount"`
		ClientsPerRoom        int                   `json:"clientsPerRoom"`
		BrowserProbeExitCodes []int                 `json:"browserProbeExitCodes"`
		DurationMS            int64                 `json:"durationMs"`
	}
	if err := decodeStrict(contents["probe_log"], &probeLog); err != nil || probeLog.CollectorVersion != raw.CollectorVersion ||
		probeLog.ReleaseCommit != raw.ReleaseCommit || !reflect.DeepEqual(probeLog.Executable, raw.Executable) ||
		probeLog.RoomCount != 2 || probeLog.ClientsPerRoom < 3 || !reflect.DeepEqual(probeLog.BrowserProbeExitCodes, []int{0, 0}) ||
		probeLog.DurationMS < int64(raw.EndedAt.Sub(raw.StartedAt)/time.Millisecond) {
		return fmt.Errorf("%w: artifact probe_log is not semantically bound to signed raw evidence", ErrInvalidObservation)
	}
	return nil
}

func verifyBuildManifestSignature(manifest Manifest, releasePublicKey ed25519.PublicKey, envelope SignatureEnvelope, raw []byte) error {
	trustedRelease, trusted := manifest.trustedSigner(SignerRoleRelease, envelope.KeyID)
	if !trusted || envelope.Algorithm != SignatureAlgorithmEd25519 || len(releasePublicKey) != ed25519.PublicKeySize || digest(releasePublicKey) != trustedRelease.PublicKeySHA256 {
		return ErrInvalidSignature
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var signedEnvelope struct {
		Payload map[string]any `json:"payload"`
	}
	if err := decoder.Decode(&signedEnvelope); err != nil || signedEnvelope.Payload == nil {
		return fmt.Errorf("%w: malformed signed build manifest payload", ErrInvalidObservation)
	}
	payloadBytes, err := json.Marshal(signedEnvelope.Payload)
	if err != nil {
		return fmt.Errorf("%w: canonicalize build manifest payload", ErrInvalidObservation)
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Value)
	if err != nil {
		signature, err = base64.RawStdEncoding.DecodeString(envelope.Value)
	}
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(releasePublicKey, payloadBytes, signature) {
		return ErrInvalidSignature
	}
	return nil
}

type Runner struct {
	Root               string
	Manifest           Manifest
	ManifestDigest     string
	CollectorPublicKey ed25519.PublicKey
	Now                func() time.Time
}

type probeResult struct {
	observation SignedObservation
	err         error
}

func (runner Runner) Run(ctx context.Context, releaseCommit string, publicKey ed25519.PublicKey, probe Probe) (Receipt, string, error) {
	if err := runner.validate(); err != nil {
		return Receipt{}, "", err
	}
	if err := ValidateReleaseCommit(releaseCommit); err != nil {
		return Receipt{}, "", err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return Receipt{}, "", ErrInvalidSignature
	}
	if !runner.matchesTrustedKey(SignerRoleRelease, publicKey) || !runner.matchesTrustedKey(SignerRoleCollector, runner.CollectorPublicKey) {
		return Receipt{}, "", ErrInvalidSignature
	}
	if probe == nil {
		return Receipt{}, "", ErrInconclusive
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(runner.Manifest.TimeoutSeconds)*time.Second)
	defer cancel()
	resultCh := make(chan probeResult, 1)
	go func() {
		observation, err := probe.Observe(probeCtx, runner.Manifest, releaseCommit)
		resultCh <- probeResult{observation: observation, err: err}
	}()
	var signed SignedObservation
	select {
	case <-probeCtx.Done():
		return Receipt{}, "", fmt.Errorf("%w: probe timeout: %v", ErrInconclusive, probeCtx.Err())
	case result := <-resultCh:
		if result.err != nil {
			if errors.Is(result.err, ErrInvalidObservation) || errors.Is(result.err, ErrInvalidSignature) || errors.Is(result.err, ErrInconclusive) {
				return Receipt{}, "", result.err
			}
			return Receipt{}, "", fmt.Errorf("%w: probe: %v", ErrInconclusive, result.err)
		}
		signed = result.observation
	}
	now := time.Now().UTC()
	if runner.Now != nil {
		now = runner.Now().UTC()
	}
	if err := verifyArtifactEvidence(runner.Root, signed.Evidence.Payload, runner.Manifest, publicKey); err != nil {
		return Receipt{}, "", err
	}
	payloadRaw, signatureRaw, err := signed.Verify(runner.Manifest, releaseCommit, publicKey, runner.CollectorPublicKey, now)
	if err != nil {
		return Receipt{}, "", err
	}
	thresholds := evaluate(runner.Manifest, signed.Payload)
	allPassed := true
	for _, result := range thresholds {
		allPassed = allPassed && result.Passed
	}
	verdict := VerdictFail
	releaseQualified := signed.Payload.EvidenceMode == EvidenceModeLive && allPassed
	if releaseQualified {
		verdict = VerdictPass
	} else if signed.Payload.EvidenceMode == EvidenceModeFixture {
		verdict = VerdictNonQualifying
	}
	receipt := Receipt{
		Schema: ReceiptSchema, GateID: runner.Manifest.ID, GeneratedAt: now,
		ReleaseCommit: releaseCommit, Backend: runner.Manifest.Backend,
		FeatureFlag: runner.Manifest.FeatureFlag, FeatureFlagValue: runner.Manifest.FeatureFlagValue,
		EvidenceMode: signed.Payload.EvidenceMode, ProbeVersion: signed.Payload.ProbeVersion,
		ManifestDigest: runner.ManifestDigest, PayloadDigest: digest(payloadRaw), SignatureDigest: digest(signatureRaw),
		SignerKeyID: signed.Signature.KeyID, SignerKeyDigest: digest(publicKey), Observation: signed.Payload, Evidence: signed.Evidence, Signature: signed.Signature,
		Thresholds: thresholds, Verdict: verdict, ReleaseQualified: releaseQualified, StopTriggered: !releaseQualified,
		StopCondition: runner.Manifest.StopCondition, RollbackCommand: runner.Manifest.RollbackCommand,
	}
	receipt.ReceiptDigest, err = receipt.canonicalDigest()
	if err != nil {
		return Receipt{}, "", err
	}
	if err := receipt.Validate(runner.Manifest, runner.ManifestDigest, releaseCommit, publicKey, runner.CollectorPublicKey, now); err != nil {
		return Receipt{}, "", err
	}
	outputPath := runner.Manifest.Receipt.LiveOutputPath
	if signed.Payload.EvidenceMode == EvidenceModeFixture {
		outputPath = runner.Manifest.Receipt.FixtureOutputPath
	}
	outputPath = strings.ReplaceAll(outputPath, "{releaseCommit}", releaseCommit)
	path, err := rootedPath(runner.Root, outputPath)
	if err != nil {
		return Receipt{}, "", err
	}
	if err := runner.consumeNonce(releaseCommit, signed.Payload.Nonce); err != nil {
		return Receipt{}, "", err
	}
	if err := writeReceiptExclusive(path, receipt); err != nil {
		return Receipt{}, "", err
	}
	return receipt, outputPath, nil
}

func (runner Runner) VerifyReceipt(path, releaseCommit string, publicKey ed25519.PublicKey) (Receipt, error) {
	if err := runner.validate(); err != nil {
		return Receipt{}, err
	}
	if err := ValidateReleaseCommit(releaseCommit); err != nil {
		return Receipt{}, err
	}
	if !runner.matchesTrustedKey(SignerRoleRelease, publicKey) || !runner.matchesTrustedKey(SignerRoleCollector, runner.CollectorPublicKey) {
		return Receipt{}, ErrInvalidSignature
	}
	resolved, err := rootedPath(runner.Root, path)
	if err != nil {
		return Receipt{}, err
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return Receipt{}, fmt.Errorf("%w: %v", ErrInconclusive, err)
	}
	var receipt Receipt
	if err := decodeStrict(raw, &receipt); err != nil {
		return Receipt{}, fmt.Errorf("%w: %v", ErrInvalidReceipt, err)
	}
	now := time.Now().UTC()
	if runner.Now != nil {
		now = runner.Now().UTC()
	}
	if err := verifyArtifactEvidence(runner.Root, receipt.Evidence.Payload, runner.Manifest, publicKey); err != nil {
		return Receipt{}, err
	}
	if err := receipt.Validate(runner.Manifest, runner.ManifestDigest, releaseCommit, publicKey, runner.CollectorPublicKey, now); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (runner Runner) validate() error {
	if strings.TrimSpace(runner.Root) == "" || !isDigest(runner.ManifestDigest) || len(runner.CollectorPublicKey) != ed25519.PublicKeySize {
		return ErrInvalidManifest
	}
	return runner.Manifest.Validate()
}

func (runner Runner) matchesTrustedKey(role string, key ed25519.PublicKey) bool {
	if len(key) != ed25519.PublicKeySize {
		return false
	}
	for _, signer := range runner.Manifest.TrustedSignerKeys {
		if signer.Role == role && signer.PublicKeySHA256 == digest(key) {
			return true
		}
	}
	return false
}

func SignObservation(payload SoakObservation, keyID string, privateKey ed25519.PrivateKey) (SignedObservation, error) {
	return SignObservationWithEvidence(payload, SignedRawEvidence{}, keyID, privateKey)
}

func SignObservationWithEvidence(payload SoakObservation, evidence SignedRawEvidence, keyID string, privateKey ed25519.PrivateKey) (SignedObservation, error) {
	if len(privateKey) != ed25519.PrivateKeySize || strings.TrimSpace(keyID) == "" {
		return SignedObservation{}, ErrInvalidSignature
	}
	raw, err := CanonicalPayload(payload)
	if err != nil {
		return SignedObservation{}, err
	}
	signature := ed25519.Sign(privateKey, raw)
	return SignedObservation{
		Schema:    SignedObservationSchema,
		Payload:   payload,
		Evidence:  evidence,
		Signature: SignatureEnvelope{Algorithm: SignatureAlgorithmEd25519, KeyID: keyID, Value: base64.StdEncoding.EncodeToString(signature)},
	}, nil
}

func evaluate(manifest Manifest, observation SoakObservation) []ThresholdResult {
	thresholds := manifest.Thresholds
	results := make([]ThresholdResult, 0, 36)
	add := func(id, operator string, actual, expected float64) {
		results = append(results, ThresholdResult{ID: id, Operator: operator, Actual: actual, Expected: expected, Passed: compare(actual, expected, operator)})
	}
	add("duration-seconds", "gte", observation.DurationSeconds, thresholds.MinimumDurationSeconds)
	add("concurrent-room-seconds", "gte", observation.ConcurrentRoomSeconds, thresholds.MinimumConcurrentRoomSeconds)
	add("room-count", "gte", float64(len(observation.Rooms)), float64(thresholds.MinimumRoomCount))
	add("participant-minutes", "gte", observation.ParticipantMinutes, thresholds.MinimumParticipantMinutes)
	for index, room := range observation.Rooms {
		prefix := fmt.Sprintf("room-%d", index+1)
		add(prefix+"-publishers", "gte", float64(room.MinimumPublishers), float64(thresholds.MinimumPublishersPerRoom))
		add(prefix+"-subscribers", "gte", float64(room.MinimumSubscribers), float64(thresholds.MinimumSubscribersPerRoom))
		add(prefix+"-active-seconds", "gte", room.ActiveSeconds, thresholds.MinimumConcurrentRoomSeconds)
	}
	add("blocked-offer-seconds", "gte", observation.HeadOfLine.BlockedOfferSeconds, thresholds.MinimumBlockedOfferSeconds)
	add("room-b-admission-samples", "gte", float64(observation.HeadOfLine.RoomBAdmissionSamples), float64(thresholds.MinimumAdmissionSamples))
	add("room-b-admission-p95", "lt", observation.HeadOfLine.RoomBAdmissionP95Seconds, thresholds.MaximumAdmissionP95Seconds)
	add("room-b-admission-failures", "eq", float64(observation.HeadOfLine.RoomBAdmissionFailures), float64(thresholds.MaximumRoomBAdmissionFailures))
	add("room-b-renegotiation-samples", "gte", float64(observation.HeadOfLine.RoomBRenegotiationSamples), float64(thresholds.MinimumRenegotiationSamples))
	add("room-b-renegotiation-p95", "lt", observation.HeadOfLine.RoomBRenegotiationP95Seconds, thresholds.MaximumRenegotiationP95Seconds)
	add("room-b-renegotiation-failures", "eq", float64(observation.HeadOfLine.RoomBRenegotiationFailures), float64(thresholds.MaximumRoomBRenegotiationFailures))
	add("packet-loss-percent", "lt", observation.MediaHealth.PacketLossPercent, thresholds.MaximumPacketLossPercent)
	add("unexpected-disconnect-participant-minute-percent", "lt", observation.MediaHealth.UnexpectedDisconnectParticipantMinutePercent, thresholds.MaximumUnexpectedDisconnectPercent)
	add("sustained-cpu-percent", "lt", observation.MediaHealth.SustainedCPUPercent, thresholds.MaximumSustainedCPUPercent)
	add("rss-container-limit-percent", "lt", observation.MediaHealth.RSSContainerLimitPercent, thresholds.MaximumRSSContainerLimitPercent)
	add("network-sample-count", "gte", float64(observation.MediaHealth.NetworkSampleCount), float64(thresholds.MinimumNetworkSampleCount))
	add("network-sample-coverage-seconds", "gte", observation.MediaHealth.NetworkSampleCoverageSeconds, thresholds.MinimumNetworkSampleCoverageSeconds)
	add("resource-sample-count", "gte", float64(observation.MediaHealth.ResourceSampleCount), float64(thresholds.MinimumResourceSampleCount))
	add("resource-sample-coverage-seconds", "gte", observation.MediaHealth.ResourceSampleCoverageSeconds, thresholds.MinimumResourceSampleCoverageSeconds)
	add("room-boundary-canaries", "gte", float64(observation.Canaries.RoomBoundaryChecks), float64(thresholds.MinimumCanaryChecksPerBoundary))
	add("sitting-boundary-canaries", "gte", float64(observation.Canaries.SittingBoundaryChecks), float64(thresholds.MinimumCanaryChecksPerBoundary))
	add("media-generation-boundary-canaries", "gte", float64(observation.Canaries.MediaGenerationBoundaryChecks), float64(thresholds.MinimumCanaryChecksPerBoundary))
	add("source-canary-observation-rate", "gte", observation.Canaries.SourceObservationRate, thresholds.MinimumSourceCanaryObservationRate)
	leaks := []struct {
		id    string
		count int
	}{
		{"track-leaks", observation.Canaries.TrackLeakCount},
		{"identity-leaks", observation.Canaries.IdentityLeakCount},
		{"chat-leaks", observation.Canaries.ChatLeakCount},
		{"scout-leaks", observation.Canaries.ScoutLeakCount},
		{"transcript-leaks", observation.Canaries.TranscriptLeakCount},
		{"recap-leaks", observation.Canaries.RecapLeakCount},
		{"artifact-leaks", observation.Canaries.ArtifactLeakCount},
		{"wrong-sitting-leaks", observation.Canaries.WrongSittingLeakCount},
		{"wrong-media-generation-leaks", observation.Canaries.WrongMediaGenerationLeakCount},
	}
	for _, leak := range leaks {
		add(leak.id, "eq", float64(leak.count), float64(thresholds.MaximumLeakCountPerSurface))
	}
	add("ai-provider-failure-injections", "gte", float64(observation.AIFailure.FailureInjections), float64(thresholds.MinimumAIProviderFailureInjections))
	add("media-interruptions-during-ai-fault", "eq", float64(observation.AIFailure.MediaInterruptions), float64(thresholds.MaximumMediaInterruptionsDuringAIFault))
	add("admission-failures-during-ai-fault", "eq", float64(observation.AIFailure.AdmissionFailures), float64(thresholds.MaximumAdmissionFailuresDuringAIFault))
	return results
}

func writeReceiptExclusive(path string, receipt Receipt) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temporary, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: receipt already exists", ErrInvalidReceipt)
		}
		return err
	}
	removeOnError := true
	defer func() {
		if removeOnError {
			_ = os.Remove(path)
		}
	}()
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return err
	}
	removeOnError = false
	return nil
}

func (runner Runner) consumeNonce(releaseCommit, nonce string) error {
	if !isDigest(nonce) {
		return ErrInvalidObservation
	}
	directoryPath := strings.ReplaceAll(runner.Manifest.Freshness.NonceLedgerPath, "{releaseCommit}", releaseCommit)
	directory, err := rootedPath(runner.Root, directoryPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	path := filepath.Join(directory, nonce+".used")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: observation nonce was already consumed", ErrInvalidObservation)
		}
		return err
	}
	if _, err := file.WriteString(releaseCommit + "\n"); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	parent, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}
