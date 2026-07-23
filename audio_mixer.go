package main

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

const (
	roomAudioSampleRate   = 48000
	roomAudioChannels     = 1
	realtimeAudioChannels = 2
	roomAudioMaxFrameMs   = 60
	roomAudioActivePeak   = 256
	// The mixer pulls one 20ms frame per source per tick, so true pre-roll
	// would require delaying every source's output to replay gated onsets.
	// Instead the gate opens earlier (lower floor + ratio, peak-assisted) and
	// releases later, so a soft "hey" onset is kept rather than clipped.
	roomAudioNoiseSeedRMS = 96.0
	roomAudioMinSpeechRMS = 160.0
	roomAudioGateRatio    = 2.5
	roomAudioGateRelease  = 25
	// roomAudioSoftClipKnee is where summed crosstalk starts to compress:
	// below it the mix is exact summation, above it a tanh knee prevents
	// wraparound while keeping each speaker at full level.
	roomAudioSoftClipKnee = 24576

	roomAudioMixInterval           = 20 * time.Millisecond
	roomAudioMixFrameSize          = roomAudioSampleRate / 50 * roomAudioChannels
	roomAudioTrailingSilenceFrames = 50
	audioSourceLimit               = roomAudioMixFrameSize * 50
)

type mixedAudioSink interface {
	WriteMixedPCM([]int16) error
}

// consentMixedAudioSink receives only the sources whose server-issued fence
// authorizes the sink's lane. The fences travel with the exact mixed frame so
// provider ingress and commit can revalidate every contributor after the
// mixer has intentionally erased speaker identity from the PCM.
type consentMixedAudioSink interface {
	WriteMixedPCMWithConsent([]int16, []ConsentFence) error
}

type audioMixerSink struct {
	sink      mixedAudioSink
	lane      ConsentLane
	authority *ConsentLaneAuthority
}

type audioActivityListener interface {
	NoteAudioActivity(time.Time, []audioActivityLevel)
}

type audioActivityLevel struct {
	TrackKey        string
	ParticipantName string
	RMS             float64
	Peak            int16
}

type audioMixer struct {
	mu                    sync.Mutex
	sinks                 map[string]audioMixerSink
	activityListener      audioActivityListener
	input                 chan audioInput
	stop                  chan struct{}
	done                  chan struct{}
	closeOnce             sync.Once
	unsubscribeWithdrawal func()
}

type audioInput struct {
	trackKey        string
	participantName string
	pcm             []int16
	fences          map[ConsentLane]ConsentFence
	consentBound    bool
	withdrawal      *ConsentWithdrawalNotice
	remove          bool
}

type audioSource struct {
	trackKey        string
	participantName string
	buffer          []int16
	noiseFloor      float64
	gateRelease     int
	laneBuffers     map[ConsentLane][]int16
	laneFences      map[ConsentLane]ConsentFence
	captureFence    ConsentFence
}

type audioSourceActivity struct {
	source          *audioSource
	trackKey        string
	participantName string
	rms             float64
	peak            int16
	active          bool
	laneFrames      map[ConsentLane][]int16
	laneFences      map[ConsentLane]ConsentFence
}

func newAudioMixer() *audioMixer {
	mixer := &audioMixer{
		sinks: map[string]audioMixerSink{},
		input: make(chan audioInput, 128),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	mixer.unsubscribeWithdrawal = subscribeConsentWithdrawals(mixer.noteWithdrawal)

	go mixer.run()
	return mixer
}

func (mixer *audioMixer) noteWithdrawal(notice ConsentWithdrawalNotice) {
	if mixer == nil {
		return
	}
	copyNotice := notice
	select {
	case <-mixer.stop:
		return
	case mixer.input <- audioInput{withdrawal: &copyNotice}:
	}
}

func (mixer *audioMixer) submit(trackKey string, participantName string, pcm []int16) {
	if mixer == nil || trackKey == "" || len(pcm) == 0 {
		return
	}

	select {
	case <-mixer.stop:
		return
	default:
	}

	select {
	case mixer.input <- audioInput{trackKey: trackKey, participantName: participantName, pcm: pcm}:
	default:
		log.Warnf("Dropping decoded audio frame for track=%s", trackKey)
	}
}

// submitWithConsent is the only live-room capture entry. Direct WebRTC RTP
// forwarding never passes through this method. Audio is queued for activity
// only after audio_capture was authorized, while transcription/model lanes
// receive aligned PCM or silence according to their own independent fences.
func (mixer *audioMixer) submitWithConsent(trackKey string, participantName string, pcm []int16, fences map[ConsentLane]ConsentFence) {
	if mixer == nil || trackKey == "" || len(pcm) == 0 {
		return
	}
	select {
	case <-mixer.stop:
		return
	default:
	}
	copiedFences := make(map[ConsentLane]ConsentFence, len(fences))
	for lane, fence := range fences {
		copiedFences[lane] = fence
	}
	copiedPCM := append([]int16(nil), pcm...)
	select {
	case mixer.input <- audioInput{trackKey: trackKey, participantName: participantName, pcm: copiedPCM, fences: copiedFences, consentBound: true}:
	default:
		log.Warnf("Dropping consent-bound decoded audio frame for track=%s", trackKey)
	}
}

func (mixer *audioMixer) removeTrack(trackKey string) {
	if mixer == nil || trackKey == "" {
		return
	}

	select {
	case <-mixer.stop:
		return
	default:
	}

	select {
	case mixer.input <- audioInput{trackKey: trackKey, remove: true}:
	default:
		log.Warnf("Dropping decoded audio remove for track=%s", trackKey)
	}
}

func (mixer *audioMixer) setSink(key string, sink mixedAudioSink) {
	if mixer == nil || key == "" {
		return
	}

	mixer.mu.Lock()
	defer mixer.mu.Unlock()

	if sink == nil {
		delete(mixer.sinks, key)
		return
	}
	mixer.sinks[key] = audioMixerSink{sink: sink}
}

func (mixer *audioMixer) setConsentSink(key string, lane ConsentLane, authority *ConsentLaneAuthority, sink mixedAudioSink) {
	if mixer == nil || key == "" {
		return
	}
	mixer.mu.Lock()
	defer mixer.mu.Unlock()
	if sink == nil {
		delete(mixer.sinks, key)
		return
	}
	mixer.sinks[key] = audioMixerSink{sink: sink, lane: lane, authority: authority}
}

func (mixer *audioMixer) setActivityListener(listener audioActivityListener) {
	if mixer == nil {
		return
	}

	mixer.mu.Lock()
	mixer.activityListener = listener
	mixer.mu.Unlock()
}

func (mixer *audioMixer) removeSink(key string) {
	if mixer == nil || key == "" {
		return
	}

	mixer.mu.Lock()
	delete(mixer.sinks, key)
	mixer.mu.Unlock()
}

func (mixer *audioMixer) close() {
	if mixer == nil {
		return
	}

	mixer.closeOnce.Do(func() {
		close(mixer.stop)
		<-mixer.done
		if mixer.unsubscribeWithdrawal != nil {
			mixer.unsubscribeWithdrawal()
		}
	})
}

func (mixer *audioMixer) run() {
	defer close(mixer.done)

	ticker := time.NewTicker(roomAudioMixInterval)
	defer ticker.Stop()

	sources := map[string]*audioSource{}
	trailingSilenceFrames := 0
	for {
		select {
		case <-mixer.stop:
			return
		case input := <-mixer.input:
			if input.withdrawal != nil {
				invalidateMixerSourcesForWithdrawal(sources, *input.withdrawal)
				continue
			}
			if input.remove {
				delete(sources, input.trackKey)
				continue
			}

			source := sources[input.trackKey]
			if source == nil {
				source = &audioSource{trackKey: input.trackKey, laneBuffers: make(map[ConsentLane][]int16), laneFences: make(map[ConsentLane]ConsentFence)}
				sources[input.trackKey] = source
			}
			if input.participantName != "" {
				source.participantName = input.participantName
			}

			if input.consentBound {
				captureFence, captureOK := input.fences[ConsentLaneAudioCapture]
				if !captureOK {
					delete(sources, input.trackKey)
					continue
				}
				if source.captureFence.policy != "" && !sameConsentFenceVersion(source.captureFence, captureFence) {
					// A regrant must never make buffered pre-withdraw PCM eligible.
					source.buffer = nil
					source.laneBuffers = make(map[ConsentLane][]int16)
					source.laneFences = make(map[ConsentLane]ConsentFence)
				}
				source.captureFence = captureFence
				source.buffer = append(source.buffer, input.pcm...)
				for _, lane := range []ConsentLane{ConsentLaneTranscription, ConsentLaneModelAnalysis} {
					if fence, ok := input.fences[lane]; ok {
						if prior, exists := source.laneFences[lane]; exists && !sameConsentFenceVersion(prior, fence) {
							source.laneBuffers[lane] = make([]int16, len(source.buffer)-len(input.pcm))
						}
						source.laneBuffers[lane] = append(source.laneBuffers[lane], input.pcm...)
						source.laneFences[lane] = fence
					} else {
						source.laneBuffers[lane] = append(source.laneBuffers[lane], make([]int16, len(input.pcm))...)
						delete(source.laneFences, lane)
					}
				}
			} else {
				source.buffer = append(source.buffer, input.pcm...)
			}
			if overflow := len(source.buffer) - audioSourceLimit; overflow > 0 {
				source.buffer = source.buffer[overflow:]
				for lane, buffer := range source.laneBuffers {
					if len(buffer) > audioSourceLimit {
						source.laneBuffers[lane] = buffer[len(buffer)-audioSourceLimit:]
					}
				}
			}
		case <-ticker.C:
			mixedPCM, activeLevels, activities := mixAudioFrameSetWithActivity(sources)
			if len(mixedPCM) == 0 {
				if trailingSilenceFrames <= 0 {
					continue
				}
				trailingSilenceFrames--
				mixedPCM = make([]int16, roomAudioMixFrameSize)
			} else {
				trailingSilenceFrames = roomAudioTrailingSilenceFrames
			}

			if len(activeLevels) > 0 {
				mixer.notifyAudioActivity(time.Now().UTC(), activeLevels)
			}
			for key, sinkConfig := range mixer.snapshotSinks() {
				sinkPCM, fences := mixedPCM, []ConsentFence(nil)
				if sinkConfig.lane != "" {
					sinkPCM, fences = mixConsentLaneFrame(activities, sinkConfig.lane, sinkConfig.authority)
					if len(sinkPCM) == 0 {
						continue
					}
				}
				var err error
				if consentSink, ok := sinkConfig.sink.(consentMixedAudioSink); ok && sinkConfig.lane != "" {
					err = consentSink.WriteMixedPCMWithConsent(sinkPCM, fences)
				} else {
					err = sinkConfig.sink.WriteMixedPCM(sinkPCM)
				}
				if err != nil {
					log.Errorf("Failed to write mixed audio sink=%s: %v", key, err)
				}
			}
		}
	}
}

func (mixer *audioMixer) snapshotSinks() map[string]audioMixerSink {
	mixer.mu.Lock()
	defer mixer.mu.Unlock()

	sinks := make(map[string]audioMixerSink, len(mixer.sinks))
	for key, sink := range mixer.sinks {
		sinks[key] = sink
	}

	return sinks
}

func (mixer *audioMixer) notifyAudioActivity(at time.Time, levels []audioActivityLevel) {
	mixer.mu.Lock()
	listener := mixer.activityListener
	mixer.mu.Unlock()
	if listener == nil {
		return
	}

	copied := append([]audioActivityLevel(nil), levels...)
	listener.NoteAudioActivity(at, copied)
}

func mixAudioFrame(sources map[string]*audioSource) []int16 {
	mixedPCM, _ := mixAudioFrameWithActivity(sources)
	return mixedPCM
}

func mixAudioFrameWithActivity(sources map[string]*audioSource) ([]int16, []audioActivityLevel) {
	mixed, levels, _ := mixAudioFrameSetWithActivity(sources)
	return mixed, levels
}

func mixAudioFrameSetWithActivity(sources map[string]*audioSource) ([]int16, []audioActivityLevel, []audioSourceActivity) {
	readySources := make([]*audioSource, 0, len(sources))
	for trackKey, source := range sources {
		if len(source.buffer) >= roomAudioMixFrameSize {
			if source.trackKey == "" {
				source.trackKey = trackKey
			}
			readySources = append(readySources, source)
		}
	}
	if len(readySources) == 0 {
		return nil, nil, nil
	}

	activeActivities := activeAudioSourceActivities(readySources)
	mixSources := make([]*audioSource, 0, len(activeActivities))
	activeLevels := make([]audioActivityLevel, 0, len(activeActivities))
	for _, activity := range activeActivities {
		mixSources = append(mixSources, activity.source)
		activeLevels = append(activeLevels, audioActivityLevel{
			TrackKey:        activity.trackKey,
			ParticipantName: activity.participantName,
			RMS:             activity.rms,
			Peak:            activity.peak,
		})
	}
	if len(mixSources) == 0 {
		for _, source := range readySources {
			source.buffer = source.buffer[roomAudioMixFrameSize:]
			for lane, buffer := range source.laneBuffers {
				if len(buffer) >= roomAudioMixFrameSize {
					source.laneBuffers[lane] = buffer[roomAudioMixFrameSize:]
				}
			}
		}
		return nil, nil, activeActivities
	}

	// Straight summation keeps each speaker at full level during crosstalk;
	// dividing by the active-source count attenuated the asker ~6dB and pumped
	// levels frame to frame as the count changed.
	mixedPCM := make([]int16, roomAudioMixFrameSize)
	for sampleIndex := range mixedPCM {
		var sampleSum int32
		for _, source := range mixSources {
			sampleSum += int32(source.buffer[sampleIndex])
		}
		mixedPCM[sampleIndex] = softClipPCM16(sampleSum)
	}

	for _, source := range readySources {
		source.buffer = source.buffer[roomAudioMixFrameSize:]
		for lane, buffer := range source.laneBuffers {
			if len(buffer) >= roomAudioMixFrameSize {
				source.laneBuffers[lane] = buffer[roomAudioMixFrameSize:]
			}
		}
	}

	return mixedPCM, activeLevels, activeActivities
}

func mixConsentLaneFrame(activities []audioSourceActivity, lane ConsentLane, authority *ConsentLaneAuthority) ([]int16, []ConsentFence) {
	eligible := make([]audioSourceActivity, 0, len(activities))
	fences := make([]ConsentFence, 0, len(activities))
	for _, activity := range activities {
		if len(activity.laneFrames[lane]) < roomAudioMixFrameSize {
			continue
		}
		fence, ok := activity.laneFences[lane]
		if !ok || authority == nil || authority.ValidateFenceLocal(fence) != nil {
			continue
		}
		eligible = append(eligible, activity)
		fences = append(fences, fence)
	}
	if len(eligible) == 0 {
		return nil, nil
	}
	mixed := make([]int16, roomAudioMixFrameSize)
	for index := range mixed {
		var sum int32
		for _, activity := range eligible {
			sum += int32(activity.laneFrames[lane][index])
		}
		mixed[index] = softClipPCM16(sum)
	}
	return mixed, fences
}

func sameConsentFenceVersion(left, right ConsentFence) bool {
	return consentBindingKey(left.binding) == consentBindingKey(right.binding) && left.policy == right.policy &&
		left.generation == right.generation && left.recordDigest == right.recordDigest
}

func invalidateMixerSourcesForWithdrawal(sources map[string]*audioSource, notice ConsentWithdrawalNotice) {
	wantBinding := consentBindingKey(notice.Binding)
	for trackKey, source := range sources {
		if source == nil {
			continue
		}
		matchesCapture := source.captureFence.policy != "" && consentBindingKey(source.captureFence.binding) == wantBinding
		if notice.Scope == ConsentAudioCapture && matchesCapture {
			delete(sources, trackKey)
			continue
		}
		for _, lane := range []ConsentLane{ConsentLaneTranscription, ConsentLaneModelAnalysis} {
			fence, ok := source.laneFences[lane]
			if !ok || consentBindingKey(fence.binding) != wantBinding {
				continue
			}
			invalidated := notice.Scope == ConsentTranscription ||
				(notice.Scope == ConsentModelAnalysis && lane == ConsentLaneModelAnalysis)
			if !invalidated {
				continue
			}
			source.laneBuffers[lane] = make([]int16, len(source.laneBuffers[lane]))
			delete(source.laneFences, lane)
		}
	}
}

func activeAudioSources(sources []*audioSource) []*audioSource {
	activities := activeAudioSourceActivities(sources)
	activeSources := make([]*audioSource, 0, len(activities))
	for _, activity := range activities {
		activeSources = append(activeSources, activity.source)
	}

	return activeSources
}

func activeAudioSourceActivities(sources []*audioSource) []audioSourceActivity {
	activeSources := make([]audioSourceActivity, 0, len(sources))
	for _, source := range sources {
		activity := sourceAudioActivity(source)
		if activity.active {
			activeSources = append(activeSources, activity)
		}
	}

	return activeSources
}

func sourceAudioActive(source *audioSource) bool {
	return sourceAudioActivity(source).active
}

func sourceAudioActivity(source *audioSource) audioSourceActivity {
	activity := audioSourceActivity{source: source, laneFrames: make(map[ConsentLane][]int16), laneFences: make(map[ConsentLane]ConsentFence)}
	if source == nil || len(source.buffer) < roomAudioMixFrameSize {
		return activity
	}
	activity.trackKey = source.trackKey
	activity.participantName = source.participantName
	for lane, buffer := range source.laneBuffers {
		if len(buffer) >= roomAudioMixFrameSize {
			activity.laneFrames[lane] = append([]int16(nil), buffer[:roomAudioMixFrameSize]...)
			if fence, ok := source.laneFences[lane]; ok {
				activity.laneFences[lane] = fence
			}
		}
	}

	rms, peak := audioFrameLevel(source.buffer[:roomAudioMixFrameSize])
	activity.rms = rms
	activity.peak = peak
	if source.noiseFloor <= 0 {
		source.noiseFloor = math.Max(roomAudioNoiseSeedRMS, math.Min(rms, roomAudioMinSpeechRMS))
	}

	threshold := math.Max(roomAudioMinSpeechRMS, source.noiseFloor*roomAudioGateRatio)
	active := rms >= threshold || (peak >= roomAudioActivePeak && rms >= threshold*0.62)
	if active {
		source.gateRelease = roomAudioGateRelease
		updateSourceNoiseFloor(source, rms, false)
		activity.active = true
		return activity
	}

	updateSourceNoiseFloor(source, rms, true)
	if source.gateRelease > 0 {
		source.gateRelease--
		activity.active = true
		return activity
	}

	return activity
}

func audioFrameLevel(frame []int16) (float64, int16) {
	if len(frame) == 0 {
		return 0, 0
	}

	var sumSquares float64
	var peak int32
	for _, sample := range frame {
		amplitude := int32(sample)
		if amplitude < 0 {
			amplitude = -amplitude
		}
		if amplitude > peak {
			peak = amplitude
		}
		normalized := float64(sample)
		sumSquares += normalized * normalized
	}

	return math.Sqrt(sumSquares / float64(len(frame))), int16(min(peak, int32(32767)))
}

func updateSourceNoiseFloor(source *audioSource, rms float64, quiet bool) {
	if source == nil {
		return
	}
	if source.noiseFloor <= 0 {
		source.noiseFloor = math.Max(roomAudioNoiseSeedRMS, rms)
		return
	}

	weight := 0.004
	if quiet || rms < source.noiseFloor*1.7 {
		weight = 0.06
	}
	source.noiseFloor = source.noiseFloor*(1-weight) + rms*weight
	if source.noiseFloor < roomAudioNoiseSeedRMS {
		source.noiseFloor = roomAudioNoiseSeedRMS
	}
}

// softClipPCM16 passes samples through unchanged up to roomAudioSoftClipKnee
// and compresses the overshoot with a tanh knee so summed speakers saturate
// smoothly instead of hard-clipping or wrapping.
func softClipPCM16(sample int32) int16 {
	magnitude := sample
	if magnitude < 0 {
		magnitude = -magnitude
	}
	if magnitude <= roomAudioSoftClipKnee {
		return int16(sample)
	}

	headroom := float64(32767 - roomAudioSoftClipKnee)
	compressed := int32(float64(roomAudioSoftClipKnee) + headroom*math.Tanh(float64(magnitude-roomAudioSoftClipKnee)/headroom))
	if sample < 0 {
		return int16(-compressed)
	}

	return int16(compressed)
}

func clampPCM16(sample int32) int16 {
	switch {
	case sample > 32767:
		return 32767
	case sample < -32768:
		return -32768
	default:
		return int16(sample)
	}
}

func roomAudioTrackKey(remoteTrack *webrtc.TrackRemote) string {
	return fmt.Sprintf("%s:%s:%d", remoteTrack.StreamID(), remoteTrack.ID(), remoteTrack.SSRC())
}

func newRoomAudioDecoder(remoteTrack *webrtc.TrackRemote) (*opusDecoder, int, error) {
	if remoteTrack.Kind() != webrtc.RTPCodecTypeAudio {
		return nil, 0, nil
	}

	codec := remoteTrack.Codec()
	if !strings.EqualFold(codec.MimeType, webrtc.MimeTypeOpus) {
		return nil, 0, fmt.Errorf("unsupported audio codec %q", codec.MimeType)
	}

	clockRate := int(codec.ClockRate)
	if clockRate == 0 {
		clockRate = roomAudioSampleRate
	}
	if clockRate != roomAudioSampleRate {
		return nil, 0, fmt.Errorf("unsupported opus clock rate %d", codec.ClockRate)
	}

	channels := normalizedRoomAudioChannels(codec.Channels)
	decoder, err := newOpusDecoder(clockRate, channels)
	if err != nil {
		return nil, 0, err
	}

	return decoder, channels, nil
}

func normalizedRoomAudioChannels(channels uint16) int {
	switch channels {
	case 1:
		return 1
	case 2:
		return 2
	default:
		return roomAudioChannels
	}
}

func roomAudioDecodeBufferSize(channels int) int {
	if channels <= 0 {
		return 0
	}

	return roomAudioSampleRate * channels * roomAudioMaxFrameMs / 1000
}

func decodeOpusToRoomPCM(decoder *opusDecoder, buffer []int16, channels int, payload []byte) ([]int16, error) {
	if decoder == nil || channels == 0 || len(payload) == 0 {
		return nil, nil
	}

	samplesPerChannel, err := decoder.Decode(payload, buffer)
	if err != nil {
		return nil, err
	}

	return normalizeRoomAudioPCM(buffer[:samplesPerChannel*channels], channels), nil
}

func normalizeRoomAudioPCM(pcm []int16, channels int) []int16 {
	switch channels {
	case 1:
		return append([]int16(nil), pcm...)
	case 2:
		monoPCM := make([]int16, 0, len(pcm)/2)
		for sampleIndex := 0; sampleIndex+1 < len(pcm); sampleIndex += 2 {
			monoPCM = append(monoPCM, clampPCM16((int32(pcm[sampleIndex])+int32(pcm[sampleIndex+1]))/2))
		}
		return monoPCM
	default:
		return nil
	}
}

func roomPCMForRealtime(pcm []int16) []int16 {
	if roomAudioChannels == realtimeAudioChannels {
		return append([]int16(nil), pcm...)
	}
	if roomAudioChannels != 1 || realtimeAudioChannels != 2 {
		return nil
	}

	stereoPCM := make([]int16, len(pcm)*realtimeAudioChannels)
	for sampleIndex, sample := range pcm {
		baseIndex := sampleIndex * realtimeAudioChannels
		stereoPCM[baseIndex] = sample
		stereoPCM[baseIndex+1] = sample
	}

	return stereoPCM
}
