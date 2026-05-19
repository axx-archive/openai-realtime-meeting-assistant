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
	roomAudioNoiseSeedRMS = 96.0
	roomAudioMinSpeechRMS = 220.0
	roomAudioGateRatio    = 3.2
	roomAudioGateRelease  = 8

	roomAudioMixInterval  = 20 * time.Millisecond
	roomAudioMixFrameSize = roomAudioSampleRate / 50 * roomAudioChannels
	audioSourceLimit      = roomAudioMixFrameSize * 50
)

type mixedAudioSink interface {
	WriteMixedPCM([]int16) error
}

type audioMixer struct {
	mu        sync.Mutex
	sinks     map[string]mixedAudioSink
	input     chan audioInput
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

type audioInput struct {
	trackKey string
	pcm      []int16
	remove   bool
}

type audioSource struct {
	buffer      []int16
	noiseFloor  float64
	gateRelease int
}

func newAudioMixer() *audioMixer {
	mixer := &audioMixer{
		sinks: map[string]mixedAudioSink{},
		input: make(chan audioInput, 128),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}

	go mixer.run()
	return mixer
}

func (mixer *audioMixer) submit(trackKey string, pcm []int16) {
	if mixer == nil || trackKey == "" || len(pcm) == 0 {
		return
	}

	select {
	case <-mixer.stop:
		return
	default:
	}

	select {
	case mixer.input <- audioInput{trackKey: trackKey, pcm: pcm}:
	default:
		log.Warnf("Dropping decoded audio frame for track=%s", trackKey)
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
	mixer.sinks[key] = sink
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
	})
}

func (mixer *audioMixer) run() {
	defer close(mixer.done)

	ticker := time.NewTicker(roomAudioMixInterval)
	defer ticker.Stop()

	sources := map[string]*audioSource{}
	for {
		select {
		case <-mixer.stop:
			return
		case input := <-mixer.input:
			if input.remove {
				delete(sources, input.trackKey)
				continue
			}

			source := sources[input.trackKey]
			if source == nil {
				source = &audioSource{}
				sources[input.trackKey] = source
			}

			source.buffer = append(source.buffer, input.pcm...)
			if overflow := len(source.buffer) - audioSourceLimit; overflow > 0 {
				source.buffer = source.buffer[overflow:]
			}
		case <-ticker.C:
			mixedPCM := mixAudioFrame(sources)
			if len(mixedPCM) == 0 {
				continue
			}

			for key, sink := range mixer.snapshotSinks() {
				if err := sink.WriteMixedPCM(mixedPCM); err != nil {
					log.Errorf("Failed to write mixed audio sink=%s: %v", key, err)
				}
			}
		}
	}
}

func (mixer *audioMixer) snapshotSinks() map[string]mixedAudioSink {
	mixer.mu.Lock()
	defer mixer.mu.Unlock()

	sinks := make(map[string]mixedAudioSink, len(mixer.sinks))
	for key, sink := range mixer.sinks {
		sinks[key] = sink
	}

	return sinks
}

func mixAudioFrame(sources map[string]*audioSource) []int16 {
	readySources := make([]*audioSource, 0, len(sources))
	for _, source := range sources {
		if len(source.buffer) >= roomAudioMixFrameSize {
			readySources = append(readySources, source)
		}
	}
	if len(readySources) == 0 {
		return nil
	}

	mixSources := activeAudioSources(readySources)
	if len(mixSources) == 0 {
		for _, source := range readySources {
			source.buffer = source.buffer[roomAudioMixFrameSize:]
		}
		return nil
	}

	mixedPCM := make([]int16, roomAudioMixFrameSize)
	for sampleIndex := range mixedPCM {
		var sampleSum int32
		for _, source := range mixSources {
			sampleSum += int32(source.buffer[sampleIndex])
		}
		mixedPCM[sampleIndex] = clampPCM16(sampleSum / int32(len(mixSources)))
	}

	for _, source := range readySources {
		source.buffer = source.buffer[roomAudioMixFrameSize:]
	}

	return mixedPCM
}

func activeAudioSources(sources []*audioSource) []*audioSource {
	activeSources := make([]*audioSource, 0, len(sources))
	for _, source := range sources {
		if sourceAudioActive(source) {
			activeSources = append(activeSources, source)
		}
	}

	return activeSources
}

func sourceAudioActive(source *audioSource) bool {
	if source == nil || len(source.buffer) < roomAudioMixFrameSize {
		return false
	}

	rms, peak := audioFrameLevel(source.buffer[:roomAudioMixFrameSize])
	if source.noiseFloor <= 0 {
		source.noiseFloor = math.Max(roomAudioNoiseSeedRMS, math.Min(rms, roomAudioMinSpeechRMS))
	}

	threshold := math.Max(roomAudioMinSpeechRMS, source.noiseFloor*roomAudioGateRatio)
	active := rms >= threshold || (peak >= roomAudioActivePeak && rms >= threshold*0.62)
	if active {
		source.gateRelease = roomAudioGateRelease
		updateSourceNoiseFloor(source, rms, false)
		return true
	}

	updateSourceNoiseFloor(source, rms, true)
	if source.gateRelease > 0 {
		source.gateRelease--
		return true
	}

	return false
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
