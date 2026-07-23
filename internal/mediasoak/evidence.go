package mediasoak

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	RawEvidenceSchema       = "bonfire.w2a.media-soak.raw-evidence.v1"
	SignedRawEvidenceSchema = "bonfire.w2a.media-soak.signed-raw-evidence.v1"
	SignerRoleRelease       = "release_operator"
	SignerRoleCollector     = "independent_collector"
)

type SignedRawEvidence struct {
	Schema    string              `json:"schema"`
	Payload   RawEvidenceManifest `json:"payload"`
	Signature SignatureEnvelope   `json:"signature"`
}

type RawEvidenceManifest struct {
	Schema           string                 `json:"schema"`
	EvidenceMode     string                 `json:"evidenceMode"`
	Synthetic        bool                   `json:"synthetic"`
	RunID            string                 `json:"runId"`
	Nonce            string                 `json:"nonce"`
	CollectorVersion string                 `json:"collectorVersion"`
	ReleaseCommit    string                 `json:"releaseCommit"`
	Backend          string                 `json:"backend"`
	FeatureFlag      string                 `json:"featureFlag"`
	FeatureFlagValue string                 `json:"featureFlagValue"`
	StartedAt        time.Time              `json:"startedAt"`
	EndedAt          time.Time              `json:"endedAt"`
	Executable       ExecutableAttestation  `json:"executable"`
	Host             HostAttestation        `json:"host"`
	RoomSamples      []RawRoomSample        `json:"roomSamples"`
	NetworkSamples   []RawNetworkSample     `json:"networkSamples"`
	ResourceSamples  []RawResourceSample    `json:"resourceSamples"`
	HeadOfLine       RawHeadOfLineEvidence  `json:"headOfLine"`
	CanaryChecks     []RawCanaryCheck       `json:"canaryChecks"`
	AIFailures       []RawAIFailureEvidence `json:"aiFailures"`
	Artifacts        []RawEvidenceArtifact  `json:"artifacts"`
}

type HostAttestation struct {
	HostDigest             string    `json:"hostDigest"`
	ContainerDigest        string    `json:"containerDigest"`
	ImageDigest            string    `json:"imageDigest"`
	GitTreeDigest          string    `json:"gitTreeDigest"`
	BuildManifestSHA256    string    `json:"buildManifestSha256"`
	ConfigSHA256           string    `json:"configSha256"`
	TransitiveInputsSHA256 string    `json:"transitiveInputsSha256"`
	SourceArchiveSHA256    string    `json:"sourceArchiveSha256"`
	CollectorNonce         string    `json:"collectorNonce"`
	ReleaseCommit          string    `json:"releaseCommit"`
	Backend                string    `json:"backend"`
	FeatureFlag            string    `json:"featureFlag"`
	FeatureFlagValue       string    `json:"featureFlagValue"`
	CapturedAt             time.Time `json:"capturedAt"`
}

// ExecutableAttestation binds the collector-signed evidence to the exact
// checked-out gate, browser probe, and gate executable blobs that produced it.
// GitBlob values are full Git object IDs (SHA-1 or SHA-256 repositories).
type ExecutableAttestation struct {
	Head                   string `json:"head"`
	GitTreeObject          string `json:"gitTreeObject"`
	GitTreeDigest          string `json:"gitTreeDigest"`
	SourceArchiveSHA256    string `json:"sourceArchiveSha256"`
	TransitiveInputsSHA256 string `json:"transitiveInputsSha256"`
	ConfigSHA256           string `json:"configSha256"`
	InputCount             int    `json:"inputCount"`
	CollectorGitBlob       string `json:"collectorGitBlob"`
	BrowserGitBlob         string `json:"browserGitBlob"`
	GateGitBlob            string `json:"gateGitBlob"`
}

type RawRoomSample struct {
	At                    time.Time `json:"at"`
	SourceDigest          string    `json:"sourceDigest"`
	RoomDigest            string    `json:"roomDigest"`
	SittingDigest         string    `json:"sittingDigest"`
	MediaGenerationDigest string    `json:"mediaGenerationDigest"`
	Publishers            int       `json:"publishers"`
	Subscribers           int       `json:"subscribers"`
	Participants          int       `json:"participants"`
}

type RawNetworkSample struct {
	At                    time.Time `json:"at"`
	SourceDigest          string    `json:"sourceDigest"`
	RoomDigest            string    `json:"roomDigest"`
	PacketsExpected       uint64    `json:"packetsExpected"`
	PacketsLost           uint64    `json:"packetsLost"`
	UnexpectedDisconnects int       `json:"unexpectedDisconnects"`
}

type RawResourceSample struct {
	At                       time.Time `json:"at"`
	SourceDigest             string    `json:"sourceDigest"`
	ProcessCPUPercent        float64   `json:"processCpuPercent"`
	RSSContainerLimitPercent float64   `json:"rssContainerLimitPercent"`
}

type RawHeadOfLineEvidence struct {
	RoomADigest         string              `json:"roomADigest"`
	RoomBDigest         string              `json:"roomBDigest"`
	BlockedAt           time.Time           `json:"blockedAt"`
	ReleasedAt          time.Time           `json:"releasedAt"`
	AdmissionAttempts   []RawLatencyAttempt `json:"admissionAttempts"`
	RenegotiationEvents []RawLatencyAttempt `json:"renegotiationEvents"`
	SourceDigest        string              `json:"sourceDigest"`
}

type RawLatencyAttempt struct {
	StartedAt    time.Time `json:"startedAt"`
	CompletedAt  time.Time `json:"completedAt"`
	Succeeded    bool      `json:"succeeded"`
	SourceDigest string    `json:"sourceDigest"`
	ClientDigest string    `json:"clientDigest"`
	OfferDigest  string    `json:"offerDigest"`
	AnswerDigest string    `json:"answerDigest"`
	OnTrackAt    time.Time `json:"onTrackAt"`
	RTPBefore    uint64    `json:"rtpPacketsBefore"`
	RTPAfter     uint64    `json:"rtpPacketsAfter"`
}

type RawCanaryCheck struct {
	At                            time.Time `json:"at"`
	SourceDigest                  string    `json:"sourceDigest"`
	Surface                       string    `json:"surface"`
	Direction                     string    `json:"direction"`
	Sentinel                      string    `json:"sentinel"`
	SourceRoomDigest              string    `json:"sourceRoomDigest"`
	ObservedRoomDigest            string    `json:"observedRoomDigest"`
	SourceSittingDigest           string    `json:"sourceSittingDigest"`
	ObservedSittingDigest         string    `json:"observedSittingDigest"`
	SourceGenerationDigest        string    `json:"sourceGenerationDigest"`
	ObservedGenerationDigest      string    `json:"observedGenerationDigest"`
	ExpectedPresent               bool      `json:"expectedPresent"`
	Observed                      bool      `json:"observed"`
	PublicationRecipientSetDigest string    `json:"publicationRecipientSetDigest"`
	DeletionRecipientSetDigest    string    `json:"deletionRecipientSetDigest"`
	PublicationRecipientCount     int       `json:"publicationRecipientCount"`
	DeletionRecipientCount        int       `json:"deletionRecipientCount"`
	IngressAcknowledged           bool      `json:"ingressAcknowledged"`
	ReadAcknowledged              bool      `json:"readAcknowledged"`
	ScrubAcknowledged             bool      `json:"scrubAcknowledged"`
	ResidueCount                  int       `json:"residueCount"`
}

type RawAIFailureEvidence struct {
	InjectedAt         time.Time           `json:"injectedAt"`
	RecoveredAt        time.Time           `json:"recoveredAt"`
	SourceDigest       string              `json:"sourceDigest"`
	MediaInterruptions int                 `json:"mediaInterruptions"`
	AdmissionFailures  int                 `json:"admissionFailures"`
	RoomBCompletions   []RawLatencyAttempt `json:"roomBCompletions"`
}

type RawEvidenceArtifact struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

func CanonicalRawEvidence(payload RawEvidenceManifest) ([]byte, error) {
	return json.Marshal(payload)
}

func SignRawEvidence(payload RawEvidenceManifest, keyID string, privateKey ed25519.PrivateKey) (SignedRawEvidence, error) {
	if len(privateKey) != ed25519.PrivateKeySize || strings.TrimSpace(keyID) == "" {
		return SignedRawEvidence{}, ErrInvalidSignature
	}
	raw, err := CanonicalRawEvidence(payload)
	if err != nil {
		return SignedRawEvidence{}, err
	}
	return SignedRawEvidence{
		Schema: SignedRawEvidenceSchema, Payload: payload,
		Signature: SignatureEnvelope{Algorithm: SignatureAlgorithmEd25519, KeyID: keyID, Value: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, raw))},
	}, nil
}

func (signed SignedRawEvidence) Verify(manifest Manifest, collectorKey ed25519.PublicKey) ([]byte, error) {
	trusted, ok := manifest.trustedSigner(SignerRoleCollector, signed.Signature.KeyID)
	if !ok || signed.Schema != SignedRawEvidenceSchema || signed.Signature.Algorithm != SignatureAlgorithmEd25519 ||
		len(collectorKey) != ed25519.PublicKeySize || digest(collectorKey) != trusted.PublicKeySHA256 {
		return nil, ErrInvalidSignature
	}
	raw, err := CanonicalRawEvidence(signed.Payload)
	if err != nil {
		return nil, err
	}
	signature, err := base64.StdEncoding.DecodeString(signed.Signature.Value)
	if err != nil {
		signature, err = base64.RawStdEncoding.DecodeString(signed.Signature.Value)
	}
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(collectorKey, raw, signature) {
		return nil, ErrInvalidSignature
	}
	return raw, nil
}

func (manifest Manifest) trustedSigner(role, keyID string) (TrustedSigner, bool) {
	for _, signer := range manifest.TrustedSignerKeys {
		if signer.Role == role && signer.KeyID == keyID {
			return signer, true
		}
	}
	return TrustedSigner{}, false
}

func RecomputeObservation(manifest Manifest, raw RawEvidenceManifest) (SoakObservation, error) {
	observation := SoakObservation{
		Schema: ObservationSchema, EvidenceMode: raw.EvidenceMode, Synthetic: raw.Synthetic, Conclusive: false,
		ReleaseCommit: raw.ReleaseCommit, Backend: raw.Backend, FeatureFlag: raw.FeatureFlag, FeatureFlagValue: raw.FeatureFlagValue,
		ProbeVersion: raw.CollectorVersion, RunID: raw.RunID, Nonce: raw.Nonce, StartedAt: raw.StartedAt.UTC(), EndedAt: raw.EndedAt.UTC(),
	}
	if err := validateRawEvidenceIdentity(manifest, raw); err != nil {
		return observation, err
	}
	rawBytes, err := CanonicalRawEvidence(raw)
	if err != nil {
		return observation, err
	}
	observation.RawEvidenceDigest = digest(rawBytes)
	hostBytes, err := json.Marshal(raw.Host)
	if err != nil {
		return observation, err
	}
	observation.HostAttestationDigest = digest(hostBytes)
	observation.ExpiresAt = raw.EndedAt.UTC().Add(time.Duration(manifest.Freshness.MaximumAgeSeconds) * time.Second)
	observation.DurationSeconds = raw.EndedAt.Sub(raw.StartedAt).Seconds()

	roomGroups := map[string][]RawRoomSample{}
	for _, sample := range raw.RoomSamples {
		roomGroups[sample.RoomDigest] = append(roomGroups[sample.RoomDigest], sample)
	}
	if len(roomGroups) != manifest.Thresholds.MinimumRoomCount {
		return observation, ErrInconclusive
	}
	roomStarts, roomEnds := []time.Time{}, []time.Time{}
	for _, samples := range roomGroups {
		sort.Slice(samples, func(i, j int) bool { return samples[i].At.Before(samples[j].At) })
		room, participantSeconds, err := summarizeRoomSamples(manifest, raw, samples)
		if err != nil {
			return observation, err
		}
		observation.Rooms = append(observation.Rooms, room)
		observation.ParticipantMinutes += participantSeconds / 60
		roomStarts, roomEnds = append(roomStarts, samples[0].At), append(roomEnds, samples[len(samples)-1].At)
	}
	sort.Slice(observation.Rooms, func(i, j int) bool { return observation.Rooms[i].RoomDigest < observation.Rooms[j].RoomDigest })
	latestStart, earliestEnd := roomStarts[0], roomEnds[0]
	for index := range roomStarts {
		if roomStarts[index].After(latestStart) {
			latestStart = roomStarts[index]
		}
		if roomEnds[index].Before(earliestEnd) {
			earliestEnd = roomEnds[index]
		}
	}
	if earliestEnd.After(latestStart) {
		observation.ConcurrentRoomSeconds = earliestEnd.Sub(latestStart).Seconds()
	}
	if err := summarizeNetwork(manifest, raw, &observation); err != nil {
		return observation, err
	}
	if err := summarizeResources(manifest, raw, &observation); err != nil {
		return observation, err
	}
	if err := summarizeHOL(raw, roomGroups, &observation); err != nil {
		return observation, err
	}
	if err := summarizeCanaries(raw, roomGroups, &observation); err != nil {
		return observation, err
	}
	for _, fault := range raw.AIFailures {
		if fault.InjectedAt.IsZero() || !fault.RecoveredAt.After(fault.InjectedAt) || fault.InjectedAt.Before(raw.StartedAt) || fault.RecoveredAt.After(raw.EndedAt) ||
			!isDigest(fault.SourceDigest) || fault.MediaInterruptions < 0 || fault.AdmissionFailures < 0 {
			return observation, ErrInvalidObservation
		}
		_, completionFailures, err := summarizeLatencyAttempts(fault.RoomBCompletions, fault.InjectedAt, fault.RecoveredAt)
		if err != nil {
			return observation, err
		}
		observation.AIFailure.FailureInjections++
		observation.AIFailure.MediaInterruptions += fault.MediaInterruptions
		observation.AIFailure.AdmissionFailures += fault.AdmissionFailures + completionFailures
	}
	observation.Conclusive = true
	return observation, nil
}

func validateRawEvidenceIdentity(manifest Manifest, raw RawEvidenceManifest) error {
	if raw.Schema != RawEvidenceSchema || (raw.EvidenceMode != EvidenceModeLive && raw.EvidenceMode != EvidenceModeFixture) ||
		(raw.EvidenceMode == EvidenceModeLive && raw.Synthetic) || (raw.EvidenceMode == EvidenceModeFixture && !raw.Synthetic) ||
		!isDigest(raw.RunID) || !isDigest(raw.Nonce) || strings.TrimSpace(raw.CollectorVersion) == "" ||
		ValidateReleaseCommit(raw.ReleaseCommit) != nil || raw.Backend != manifest.Backend || raw.FeatureFlag != manifest.FeatureFlag || raw.FeatureFlagValue != manifest.FeatureFlagValue ||
		raw.StartedAt.IsZero() || raw.EndedAt.IsZero() || !raw.EndedAt.After(raw.StartedAt) || raw.Host.CapturedAt.Before(raw.StartedAt) || raw.Host.CapturedAt.After(raw.EndedAt) ||
		raw.Host.ReleaseCommit != raw.ReleaseCommit || raw.Host.Backend != raw.Backend || raw.Host.FeatureFlag != raw.FeatureFlag || raw.Host.FeatureFlagValue != raw.FeatureFlagValue ||
		!isDigest(raw.Host.HostDigest) || !isDigest(raw.Host.ContainerDigest) || !isDigest(raw.Host.ImageDigest) ||
		!isDigest(raw.Host.GitTreeDigest) || !isDigest(raw.Host.BuildManifestSHA256) || !isDigest(raw.Host.ConfigSHA256) || !isDigest(raw.Host.TransitiveInputsSHA256) || !isDigest(raw.Host.SourceArchiveSHA256) || raw.Host.CollectorNonce != raw.Nonce ||
		raw.Executable.Head != raw.ReleaseCommit || !isGitObjectID(raw.Executable.GitTreeObject) || !isDigest(raw.Executable.GitTreeDigest) || raw.Executable.GitTreeDigest != raw.Host.GitTreeDigest ||
		!isDigest(raw.Executable.SourceArchiveSHA256) || !isDigest(raw.Executable.TransitiveInputsSHA256) || !isDigest(raw.Executable.ConfigSHA256) || raw.Executable.InputCount <= 0 ||
		raw.Executable.ConfigSHA256 != raw.Host.ConfigSHA256 || raw.Executable.TransitiveInputsSHA256 != raw.Host.TransitiveInputsSHA256 || raw.Executable.SourceArchiveSHA256 != raw.Host.SourceArchiveSHA256 ||
		!isGitObjectID(raw.Executable.CollectorGitBlob) ||
		!isGitObjectID(raw.Executable.BrowserGitBlob) || !isGitObjectID(raw.Executable.GateGitBlob) {
		return ErrInvalidObservation
	}
	required := map[string]bool{
		"browser_events": false, "webrtc_stats": false, "container_metrics": false,
		"fault_log": false, "config_attestation": false, "probe_log": false,
		"probe_image": false, "build_manifest": false,
	}
	seenPaths := map[string]bool{}
	for _, artifact := range raw.Artifacts {
		if !isDigest(artifact.SHA256) || artifact.Bytes <= 0 || strings.TrimSpace(artifact.Kind) == "" ||
			validateRelativePath(artifact.Path) != nil || seenPaths[artifact.Path] {
			return ErrInvalidObservation
		}
		seenPaths[artifact.Path] = true
		if _, ok := required[artifact.Kind]; ok {
			required[artifact.Kind] = true
		}
	}
	for _, present := range required {
		if !present {
			return ErrInconclusive
		}
	}
	return nil
}

func isGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func summarizeRoomSamples(manifest Manifest, raw RawEvidenceManifest, samples []RawRoomSample) (RoomScopeEvidence, float64, error) {
	if len(samples) < manifest.Collector.MinimumRawSamples {
		return RoomScopeEvidence{}, 0, ErrInconclusive
	}
	first := samples[0]
	room := RoomScopeEvidence{RoomDigest: first.RoomDigest, SittingDigest: first.SittingDigest, MediaGenerationDigest: first.MediaGenerationDigest, MinimumPublishers: math.MaxInt, MinimumSubscribers: math.MaxInt}
	participantSeconds := 0.0
	for index, sample := range samples {
		if sample.At.Before(raw.StartedAt) || sample.At.After(raw.EndedAt) || sample.RoomDigest != first.RoomDigest || sample.SittingDigest != first.SittingDigest || sample.MediaGenerationDigest != first.MediaGenerationDigest ||
			!isDigest(sample.SourceDigest) || !isDigest(sample.RoomDigest) || !isDigest(sample.SittingDigest) || !isDigest(sample.MediaGenerationDigest) || sample.Publishers < 0 || sample.Subscribers < 0 ||
			sample.Participants < maxInt(sample.Publishers, sample.Subscribers) || sample.Participants > sample.Publishers+sample.Subscribers {
			return RoomScopeEvidence{}, 0, ErrInvalidObservation
		}
		room.MinimumPublishers = minInt(room.MinimumPublishers, sample.Publishers)
		room.MinimumSubscribers = minInt(room.MinimumSubscribers, sample.Subscribers)
		if index > 0 {
			gap := sample.At.Sub(samples[index-1].At).Seconds()
			if gap <= 0 || gap > manifest.Collector.MaximumSampleGapSeconds {
				return RoomScopeEvidence{}, 0, ErrInconclusive
			}
			participantSeconds += gap * float64(minInt(sample.Participants, samples[index-1].Participants))
		}
	}
	room.ActiveSeconds = samples[len(samples)-1].At.Sub(samples[0].At).Seconds()
	if samples[0].At.Sub(raw.StartedAt).Seconds() > manifest.Collector.MaximumSampleGapSeconds ||
		raw.EndedAt.Sub(samples[len(samples)-1].At).Seconds() > manifest.Collector.MaximumSampleGapSeconds {
		return RoomScopeEvidence{}, 0, ErrInconclusive
	}
	return room, participantSeconds, nil
}

func summarizeNetwork(manifest Manifest, raw RawEvidenceManifest, observation *SoakObservation) error {
	groups := map[string][]RawNetworkSample{}
	for _, sample := range raw.NetworkSamples {
		groups[sample.RoomDigest] = append(groups[sample.RoomDigest], sample)
	}
	if len(groups) != len(observation.Rooms) {
		return ErrInconclusive
	}
	var expected, lost uint64
	disconnectedParticipantMinutes := 0.0
	minimumCoverage := math.MaxFloat64
	for _, room := range observation.Rooms {
		samples := groups[room.RoomDigest]
		if len(samples) < manifest.Collector.MinimumRawSamples {
			return ErrInconclusive
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i].At.Before(samples[j].At) })
		for index, sample := range samples {
			if sample.At.Before(raw.StartedAt) || sample.At.After(raw.EndedAt) || !isDigest(sample.SourceDigest) ||
				!isDigest(sample.RoomDigest) || sample.PacketsLost > sample.PacketsExpected || sample.UnexpectedDisconnects < 0 {
				return ErrInvalidObservation
			}
			if index > 0 {
				previous := samples[index-1]
				gap := sample.At.Sub(previous.At).Seconds()
				if gap <= 0 || gap > manifest.Collector.MaximumSampleGapSeconds ||
					sample.PacketsExpected < previous.PacketsExpected || sample.PacketsLost < previous.PacketsLost {
					return ErrInconclusive
				}
				expected += sample.PacketsExpected - previous.PacketsExpected
				lost += sample.PacketsLost - previous.PacketsLost
				disconnectedParticipantMinutes += gap / 60 * float64(maxInt(sample.UnexpectedDisconnects, previous.UnexpectedDisconnects))
			}
		}
		coverage := samples[len(samples)-1].At.Sub(samples[0].At).Seconds()
		if samples[0].At.Sub(raw.StartedAt).Seconds() > manifest.Collector.MaximumSampleGapSeconds ||
			raw.EndedAt.Sub(samples[len(samples)-1].At).Seconds() > manifest.Collector.MaximumSampleGapSeconds {
			return ErrInconclusive
		}
		minimumCoverage = math.Min(minimumCoverage, coverage)
	}
	if expected == 0 || observation.ParticipantMinutes <= 0 {
		return ErrInconclusive
	}
	observation.MediaHealth.PacketLossPercent = float64(lost) / float64(expected) * 100
	observation.MediaHealth.UnexpectedDisconnectParticipantMinutePercent = disconnectedParticipantMinutes / observation.ParticipantMinutes * 100
	observation.MediaHealth.NetworkSampleCount = len(raw.NetworkSamples)
	observation.MediaHealth.NetworkSampleCoverageSeconds = minimumCoverage
	return nil
}

func summarizeResources(manifest Manifest, raw RawEvidenceManifest, observation *SoakObservation) error {
	if len(raw.ResourceSamples) < manifest.Collector.MinimumRawSamples {
		return ErrInconclusive
	}
	sort.Slice(raw.ResourceSamples, func(i, j int) bool { return raw.ResourceSamples[i].At.Before(raw.ResourceSamples[j].At) })
	for index, sample := range raw.ResourceSamples {
		if sample.At.Before(raw.StartedAt) || sample.At.After(raw.EndedAt) || !isDigest(sample.SourceDigest) || !finite(sample.ProcessCPUPercent) || !finite(sample.RSSContainerLimitPercent) ||
			sample.ProcessCPUPercent < 0 || sample.ProcessCPUPercent > 100 || sample.RSSContainerLimitPercent < 0 || sample.RSSContainerLimitPercent > 100 {
			return ErrInvalidObservation
		}
		if index > 0 && sample.At.Sub(raw.ResourceSamples[index-1].At).Seconds() > manifest.Collector.MaximumSampleGapSeconds {
			return ErrInconclusive
		}
		observation.MediaHealth.SustainedCPUPercent = math.Max(observation.MediaHealth.SustainedCPUPercent, sample.ProcessCPUPercent)
		observation.MediaHealth.RSSContainerLimitPercent = math.Max(observation.MediaHealth.RSSContainerLimitPercent, sample.RSSContainerLimitPercent)
	}
	observation.MediaHealth.ResourceSampleCount = len(raw.ResourceSamples)
	observation.MediaHealth.ResourceSampleCoverageSeconds = raw.ResourceSamples[len(raw.ResourceSamples)-1].At.Sub(raw.ResourceSamples[0].At).Seconds()
	if raw.ResourceSamples[0].At.Sub(raw.StartedAt).Seconds() > manifest.Collector.MaximumSampleGapSeconds ||
		raw.EndedAt.Sub(raw.ResourceSamples[len(raw.ResourceSamples)-1].At).Seconds() > manifest.Collector.MaximumSampleGapSeconds {
		return ErrInconclusive
	}
	return nil
}

func summarizeHOL(raw RawEvidenceManifest, rooms map[string][]RawRoomSample, observation *SoakObservation) error {
	hol := raw.HeadOfLine
	if !isDigest(hol.SourceDigest) || !isDigest(hol.RoomADigest) || !isDigest(hol.RoomBDigest) || hol.RoomADigest == hol.RoomBDigest ||
		rooms[hol.RoomADigest] == nil || rooms[hol.RoomBDigest] == nil || hol.BlockedAt.Before(raw.StartedAt) ||
		hol.ReleasedAt.After(raw.EndedAt) || !hol.ReleasedAt.After(hol.BlockedAt) {
		return ErrInvalidObservation
	}
	admissionSeconds, admissionFailures, err := summarizeLatencyAttempts(hol.AdmissionAttempts, hol.BlockedAt, hol.ReleasedAt)
	if err != nil {
		return err
	}
	renegotiationSeconds, renegotiationFailures, err := summarizeLatencyAttempts(hol.RenegotiationEvents, hol.BlockedAt, hol.ReleasedAt)
	if err != nil {
		return err
	}
	observation.HeadOfLine = HeadOfLineEvidence{
		BlockedOfferSeconds: hol.ReleasedAt.Sub(hol.BlockedAt).Seconds(), RoomBAdmissionSamples: len(admissionSeconds),
		RoomBAdmissionP95Seconds: percentile95(admissionSeconds), RoomBAdmissionFailures: admissionFailures,
		RoomBRenegotiationSamples: len(renegotiationSeconds), RoomBRenegotiationP95Seconds: percentile95(renegotiationSeconds), RoomBRenegotiationFailures: renegotiationFailures,
	}
	return nil
}

func summarizeLatencyAttempts(attempts []RawLatencyAttempt, blockedAt, releasedAt time.Time) ([]float64, int, error) {
	seconds := make([]float64, 0, len(attempts))
	failures := 0
	clients := map[string]bool{}
	for _, attempt := range attempts {
		if !isDigest(attempt.SourceDigest) || !isDigest(attempt.ClientDigest) || !isDigest(attempt.OfferDigest) || !isDigest(attempt.AnswerDigest) ||
			attempt.OfferDigest != attempt.AnswerDigest ||
			attempt.StartedAt.Before(blockedAt) || attempt.CompletedAt.After(releasedAt) || !attempt.OnTrackAt.After(attempt.StartedAt) ||
			attempt.OnTrackAt.After(attempt.CompletedAt) || !attempt.CompletedAt.After(attempt.StartedAt) || attempt.RTPAfter <= attempt.RTPBefore {
			return nil, 0, ErrInvalidObservation
		}
		seconds = append(seconds, attempt.CompletedAt.Sub(attempt.StartedAt).Seconds())
		if clients[attempt.ClientDigest] {
			return nil, 0, ErrInvalidObservation
		}
		clients[attempt.ClientDigest] = true
		if !attempt.Succeeded {
			failures++
		}
	}
	if len(clients) < 3 {
		return nil, 0, ErrInconclusive
	}
	return seconds, failures, nil
}

func summarizeCanaries(raw RawEvidenceManifest, rooms map[string][]RawRoomSample, observation *SoakObservation) error {
	if len(raw.CanaryChecks) == 0 {
		return ErrInconclusive
	}
	present, observed := 0, 0
	coverage := map[string]bool{}
	for _, check := range raw.CanaryChecks {
		if check.At.Before(raw.StartedAt) || check.At.After(raw.EndedAt) || !isDigest(check.SourceDigest) || !validCanarySurface(check.Surface) ||
			!isDigest(check.SourceRoomDigest) || !isDigest(check.ObservedRoomDigest) || !isDigest(check.SourceSittingDigest) || !isDigest(check.ObservedSittingDigest) ||
			!isDigest(check.SourceGenerationDigest) || !isDigest(check.ObservedGenerationDigest) || rooms[check.SourceRoomDigest] == nil || check.ResidueCount != 0 {
			return ErrInvalidObservation
		}
		if !check.IngressAcknowledged || !check.ReadAcknowledged || !check.ScrubAcknowledged {
			return ErrInconclusive
		}
		publicationSurface := check.Surface == "chat" || check.Surface == "recap" || check.Surface == "artifact"
		if publicationSurface {
			if check.PublicationRecipientCount <= 0 || check.DeletionRecipientCount <= 0 ||
				!isDigest(check.PublicationRecipientSetDigest) || !isDigest(check.DeletionRecipientSetDigest) ||
				check.PublicationRecipientCount != check.DeletionRecipientCount || check.PublicationRecipientSetDigest != check.DeletionRecipientSetDigest {
				return ErrInvalidObservation
			}
		} else if check.PublicationRecipientCount != 0 || check.DeletionRecipientCount != 0 || check.PublicationRecipientSetDigest != "" || check.DeletionRecipientSetDigest != "" {
			return ErrInvalidObservation
		}
		if check.ExpectedPresent {
			if check.Sentinel != "current" || check.SourceRoomDigest != check.ObservedRoomDigest ||
				check.SourceSittingDigest != check.ObservedSittingDigest || check.SourceGenerationDigest != check.ObservedGenerationDigest {
				return ErrInvalidObservation
			}
			present++
			if check.Observed {
				observed++
			}
		} else {
			switch check.Sentinel {
			case "prior_sitting":
				if check.SourceRoomDigest != check.ObservedRoomDigest || check.SourceSittingDigest == check.ObservedSittingDigest || check.SourceGenerationDigest != check.ObservedGenerationDigest {
					return ErrInvalidObservation
				}
				observation.Canaries.SittingBoundaryChecks++
			case "prior_generation":
				if check.SourceRoomDigest != check.ObservedRoomDigest || check.SourceSittingDigest != check.ObservedSittingDigest || check.SourceGenerationDigest == check.ObservedGenerationDigest {
					return ErrInvalidObservation
				}
				observation.Canaries.MediaGenerationBoundaryChecks++
			case "unrelated_room":
				if check.SourceRoomDigest == check.ObservedRoomDigest || check.SourceSittingDigest != check.ObservedSittingDigest || check.SourceGenerationDigest != check.ObservedGenerationDigest {
					return ErrInvalidObservation
				}
				observation.Canaries.RoomBoundaryChecks++
			default:
				return ErrInvalidObservation
			}
			if check.Observed {
				incrementLeak(&observation.Canaries, check.Surface)
			}
		}
		if check.Direction != "a_to_b" && check.Direction != "b_to_a" {
			return ErrInvalidObservation
		}
		coverage[check.Surface+"|"+check.Direction+"|"+check.Sentinel] = true
		if check.Observed && check.SourceSittingDigest != check.ObservedSittingDigest {
			observation.Canaries.WrongSittingLeakCount++
		}
		if check.Observed && check.SourceGenerationDigest != check.ObservedGenerationDigest {
			observation.Canaries.WrongMediaGenerationLeakCount++
		}
	}
	for _, surface := range []string{"track", "chat", "scout", "transcript", "recap", "artifact"} {
		for _, direction := range []string{"a_to_b", "b_to_a"} {
			for _, sentinel := range []string{"current", "prior_sitting", "prior_generation", "unrelated_room"} {
				if !coverage[surface+"|"+direction+"|"+sentinel] {
					return ErrInconclusive
				}
			}
		}
	}
	if observation.Canaries.RoomBoundaryChecks == 0 || observation.Canaries.SittingBoundaryChecks == 0 || observation.Canaries.MediaGenerationBoundaryChecks == 0 {
		return ErrInconclusive
	}
	if present == 0 {
		return ErrInconclusive
	}
	observation.Canaries.SourceObservationRate = float64(observed) / float64(present)
	return nil
}

func validCanarySurface(value string) bool {
	switch value {
	case "track", "chat", "scout", "transcript", "recap", "artifact":
		return true
	default:
		return false
	}
}

func incrementLeak(canaries *CanaryEvidence, surface string) {
	switch surface {
	case "track":
		canaries.TrackLeakCount++
	case "identity":
		canaries.IdentityLeakCount++
	case "chat":
		canaries.ChatLeakCount++
	case "scout":
		canaries.ScoutLeakCount++
	case "transcript":
		canaries.TranscriptLeakCount++
	case "recap":
		canaries.RecapLeakCount++
	case "artifact":
		canaries.ArtifactLeakCount++
	}
}

func percentile95(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	index := int(math.Ceil(0.95*float64(len(copyValues)))) - 1
	if index < 0 {
		index = 0
	}
	return copyValues[index]
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
