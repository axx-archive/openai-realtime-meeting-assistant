package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	dictationConcurrentPerUser = 1
	dictationRequestsPerHour   = 30
	dictationSecondsPerDay     = 3600
	dictationQuotaRetention    = 48 * time.Hour
	dictationDurationProbeMax  = 2 << 20
	dictationMetadataMaxBytes  = 8 << 10
)

var (
	errDictationConcurrent = errors.New("a dictation is already being transcribed")
	errDictationRate       = errors.New("dictation request limit reached")
	errDictationBudget     = errors.New("daily dictation audio budget reached")
	errDictationTooLarge   = errors.New("dictation exceeds the accepted size or duration")
	errDictationUpload     = errors.New("invalid dictation upload")
)

type dictationUsageWindow struct {
	active          int
	hourStarted     time.Time
	hourRequests    int
	dayStarted      time.Time
	dayAudioSeconds float64
	lastSeen        time.Time
}

// dictationQuotaManager is the server-side cost gate for the file-at-a-time
// transcription lane. It is intentionally keyed by the authenticated account,
// not by an IP or a client-supplied identifier. The limits bound both accidental
// retry storms and deliberate member abuse before a provider request is made.
type dictationQuotaManager struct {
	mu    sync.Mutex
	users map[string]*dictationUsageWindow
}

func newDictationQuotaManager() *dictationQuotaManager {
	return &dictationQuotaManager{users: map[string]*dictationUsageWindow{}}
}

func (manager *dictationQuotaManager) acquire(userKey string, now time.Time) (func(), error) {
	userKey = normalizeAccountEmail(userKey)
	if userKey == "" {
		return nil, errDictationRate
	}
	now = now.UTC()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.pruneLocked(now)
	window := manager.users[userKey]
	if window == nil {
		window = &dictationUsageWindow{}
		manager.users[userKey] = window
	}
	resetDictationWindows(window, now)
	window.lastSeen = now
	if window.active >= dictationConcurrentPerUser {
		return nil, errDictationConcurrent
	}
	if window.hourRequests >= dictationRequestsPerHour {
		return nil, errDictationRate
	}
	window.active++
	window.hourRequests++

	var once sync.Once
	return func() {
		once.Do(func() {
			manager.mu.Lock()
			defer manager.mu.Unlock()
			if current := manager.users[userKey]; current != nil {
				if current.active > 0 {
					current.active--
				}
				current.lastSeen = time.Now().UTC()
			}
		})
	}, nil
}

func (manager *dictationQuotaManager) charge(userKey string, now time.Time, seconds float64) error {
	userKey = normalizeAccountEmail(userKey)
	if userKey == "" || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return errDictationBudget
	}
	now = now.UTC()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	window := manager.users[userKey]
	if window == nil {
		return errDictationBudget
	}
	resetDictationWindows(window, now)
	window.lastSeen = now
	if window.dayAudioSeconds+seconds > dictationSecondsPerDay {
		return errDictationBudget
	}
	// Reserve the audio budget before the provider call. Failed wire attempts
	// are not refunded because they can still consume provider processing.
	window.dayAudioSeconds += seconds
	return nil
}

func resetDictationWindows(window *dictationUsageWindow, now time.Time) {
	if window.hourStarted.IsZero() || now.Before(window.hourStarted) || now.Sub(window.hourStarted) >= time.Hour {
		window.hourStarted = now
		window.hourRequests = 0
	}
	if window.dayStarted.IsZero() || now.Before(window.dayStarted) || now.Sub(window.dayStarted) >= 24*time.Hour {
		window.dayStarted = now
		window.dayAudioSeconds = 0
	}
}

func (manager *dictationQuotaManager) pruneLocked(now time.Time) {
	if len(manager.users) < 2048 {
		return
	}
	for key, window := range manager.users {
		if window.active == 0 && !window.lastSeen.IsZero() && now.Sub(window.lastSeen) >= dictationQuotaRetention {
			delete(manager.users, key)
		}
	}
}

var dictationQuotas = newDictationQuotaManager()

// spoolDictation copies a bounded upload into a private temporary file. This
// avoids retaining a 25 MB recording plus a second multipart copy in the Go
// heap while still giving duration probes and the provider uploader a seekable
// source. The caller owns closing and removing the returned file.
func spoolDictation(source io.Reader) (*os.File, int64, error) {
	temporary, err := os.CreateTemp("", "meetingassist-dictation-*")
	if err != nil {
		return nil, 0, fmt.Errorf("create private dictation spool: %w", err)
	}
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporary.Name())
	}
	written, err := io.Copy(temporary, io.LimitReader(source, dictationMaxBytes+1))
	if err != nil {
		cleanup()
		return nil, 0, fmt.Errorf("spool dictation: %w", err)
	}
	if written == 0 {
		cleanup()
		return nil, 0, io.ErrUnexpectedEOF
	}
	if written > dictationMaxBytes {
		cleanup()
		return nil, written, errDictationTooLarge
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return nil, 0, fmt.Errorf("sync dictation spool: %w", err)
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, 0, fmt.Errorf("rewind dictation spool: %w", err)
	}
	return temporary, written, nil
}

// streamDictationUpload consumes the incoming multipart body exactly once and
// writes the audio part directly to its provider/duration spool. It deliberately
// does not call Request.ParseMultipartForm: that API may retain values in heap
// and makes an intermediate upload file before this lane creates its own.
func streamDictationUpload(request *http.Request) (audio *os.File, filename, contextValue, threadID string, err error) {
	reader, err := request.MultipartReader()
	if err != nil {
		return nil, "", "", "", fmt.Errorf("%w: multipart body required", errDictationUpload)
	}
	cleanup := func() {
		if audio != nil {
			_ = audio.Close()
			_ = os.Remove(audio.Name())
			audio = nil
		}
	}
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			cleanup()
			return nil, "", "", "", nextErr
		}
		name := part.FormName()
		switch name {
		case "audio":
			if audio != nil {
				_ = part.Close()
				cleanup()
				return nil, "", "", "", fmt.Errorf("%w: more than one audio field", errDictationUpload)
			}
			audio, _, err = spoolDictation(part)
			filename = part.FileName()
			_ = part.Close()
			if err != nil {
				cleanup()
				return nil, "", "", "", err
			}
		case "context", "threadId":
			value, valueErr := readDictationMetadata(part)
			_ = part.Close()
			if valueErr != nil {
				cleanup()
				return nil, "", "", "", valueErr
			}
			if name == "context" {
				contextValue = value
			} else {
				threadID = value
			}
		default:
			// Drain unknown fields without materializing them. This preserves the
			// public contract's forward compatibility without making an arbitrary
			// extra field a heap allocation or an audio-provider input.
			_, drainErr := io.Copy(io.Discard, part)
			_ = part.Close()
			if drainErr != nil {
				cleanup()
				return nil, "", "", "", drainErr
			}
		}
	}
	if audio == nil {
		return nil, "", "", "", fmt.Errorf("%w: audio field required", errDictationUpload)
	}
	return audio, filename, contextValue, threadID, nil
}

func readDictationMetadata(part *multipart.Part) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(part, dictationMetadataMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > dictationMetadataMaxBytes {
		return "", fmt.Errorf("%w: metadata field too large", errDictationUpload)
	}
	return string(raw), nil
}

// serverDerivedDictationSeconds reads duration from the uploaded media
// container. The client duration is never an authority input. If a supported
// container does not expose a trustworthy duration, the request is charged the
// full accepted 10-minute bound: conservative but never an undercount.
func serverDerivedDictationSeconds(file *os.File, filename string) (seconds float64, estimated bool, err error) {
	if file == nil {
		return 0, false, io.ErrUnexpectedEOF
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, false, err
	}
	probe, err := io.ReadAll(io.LimitReader(file, dictationDurationProbeMax))
	if err != nil {
		return 0, false, err
	}
	extension := strings.ToLower(filepath.Ext(filename))
	var parsed float64
	switch {
	case len(probe) >= 12 && bytes.Equal(probe[:4], []byte("RIFF")) && bytes.Equal(probe[8:12], []byte("WAVE")):
		parsed = wavDurationSeconds(probe)
	case len(probe) >= 4 && bytes.Equal(probe[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}), extension == ".webm":
		parsed = webMDurationSeconds(probe)
	case extension == ".m4a", extension == ".mp4", extension == ".mov", len(probe) >= 12 && bytes.Equal(probe[4:8], []byte("ftyp")):
		parsed = mp4DurationSeconds(file)
	}
	if parsed <= 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return 0, false, err
		}
		return dictationMaxSeconds, true, nil
	}
	// Allow a small recorder/container rounding tail at the exact client cap.
	if parsed > dictationMaxSeconds+5 {
		return 0, false, errDictationTooLarge
	}
	if parsed > dictationMaxSeconds {
		parsed = dictationMaxSeconds
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, false, err
	}
	return parsed, false, nil
}

func wavDurationSeconds(raw []byte) float64 {
	var byteRate uint32
	for offset := 12; offset+8 <= len(raw); {
		chunkSize := uint64(binary.LittleEndian.Uint32(raw[offset+4 : offset+8]))
		payload := offset + 8
		if payload > len(raw) {
			return 0
		}
		tag := string(raw[offset : offset+4])
		// A data chunk's payload can legitimately extend beyond the bounded probe.
		// Its declared size is all duration derivation needs, so return before
		// validating payload availability. Every other skipped chunk must fit.
		if tag == "data" {
			if byteRate == 0 {
				return 0
			}
			return float64(chunkSize) / float64(byteRate)
		}
		if chunkSize > uint64(len(raw)-payload) {
			return 0
		}
		if tag == "fmt " && chunkSize >= 16 {
			byteRate = binary.LittleEndian.Uint32(raw[payload+8 : payload+12])
		}
		next := uint64(payload) + chunkSize + chunkSize%2
		if next > uint64(len(raw)) {
			return 0
		}
		offset = int(next)
	}
	return 0
}

func webMDurationSeconds(raw []byte) float64 {
	// Duration (0x4489) is only authoritative when it is a child of Info
	// (0x1549a966), itself a child of the top-level Segment (0x18538067).
	// Searching for byte sequences admits a forged Duration inside arbitrary
	// codec data and can undercharge an otherwise oversized recording.
	for offset := 0; offset < len(raw); {
		id, payload, next, _, ok := nextEBMLElement(raw, offset)
		if !ok {
			return 0
		}
		if id == 0x18538067 { // Segment
			return webMSegmentDurationSeconds(payload)
		}
		offset = next
	}
	return 0
}

func webMSegmentDurationSeconds(segment []byte) float64 {
	for offset := 0; offset < len(segment); {
		id, payload, next, unknown, ok := nextEBMLElement(segment, offset)
		if !ok || unknown {
			return 0
		}
		if id == 0x1549a966 { // Info
			return webMInfoDurationSeconds(payload)
		}
		offset = next
	}
	return 0
}

func webMInfoDurationSeconds(info []byte) float64 {
	timecodeScale := uint64(1_000_000)
	var duration float64
	for offset := 0; offset < len(info); {
		id, payload, next, unknown, ok := nextEBMLElement(info, offset)
		if !ok || unknown {
			return 0
		}
		switch id {
		case 0x2ad7b1: // TimecodeScale
			if len(payload) == 0 || len(payload) > 8 {
				return 0
			}
			timecodeScale = 0
			for _, value := range payload {
				timecodeScale = timecodeScale<<8 | uint64(value)
			}
		case 0x4489: // Duration
			switch len(payload) {
			case 4:
				duration = float64(math.Float32frombits(binary.BigEndian.Uint32(payload)))
			case 8:
				duration = math.Float64frombits(binary.BigEndian.Uint64(payload))
			default:
				return 0
			}
		}
		offset = next
	}
	return duration * float64(timecodeScale) / float64(time.Second)
}

// nextEBMLElement returns a structurally bounded EBML element at offset. It
// accepts an unknown-size Segment only; callers fail closed for unknown nested
// elements because their extent cannot be verified in a bounded probe.
func nextEBMLElement(raw []byte, offset int) (id uint32, payload []byte, next int, unknown bool, ok bool) {
	if offset < 0 || offset >= len(raw) {
		return 0, nil, 0, false, false
	}
	idLength := ebmlVINTLength(raw[offset])
	if idLength == 0 || idLength > 4 || offset+idLength >= len(raw) {
		return 0, nil, 0, false, false
	}
	for _, value := range raw[offset : offset+idLength] {
		id = id<<8 | uint32(value)
	}
	sizeOffset := offset + idLength
	sizeLength := ebmlVINTLength(raw[sizeOffset])
	if sizeLength == 0 || sizeOffset+sizeLength > len(raw) {
		return 0, nil, 0, false, false
	}
	size := uint64(raw[sizeOffset] & (byte(0x80)>>(sizeLength-1) - 1))
	for _, value := range raw[sizeOffset+1 : sizeOffset+sizeLength] {
		size = size<<8 | uint64(value)
	}
	unknownSize := uint64(1)<<(7*sizeLength) - 1
	payloadStart := sizeOffset + sizeLength
	if size == unknownSize {
		return id, raw[payloadStart:], len(raw), true, true
	}
	if size > uint64(len(raw)-payloadStart) {
		return 0, nil, 0, false, false
	}
	payloadEnd := payloadStart + int(size)
	return id, raw[payloadStart:payloadEnd], payloadEnd, false, true
}

func ebmlVINTLength(first byte) int {
	for width := 1; width <= 8; width++ {
		if first&(byte(0x80)>>(width-1)) != 0 {
			return width
		}
	}
	return 0
}

func mp4DurationSeconds(file *os.File) float64 {
	info, err := file.Stat()
	if err != nil || info.Size() < 8 {
		return 0
	}
	moovStart, moovEnd, ok := findMP4Atom(file, 0, info.Size(), "moov")
	if !ok {
		return 0
	}
	mvhdStart, mvhdEnd, ok := findMP4Atom(file, moovStart, moovEnd, "mvhd")
	if !ok || mvhdEnd-mvhdStart < 20 {
		return 0
	}
	if _, err := file.Seek(mvhdStart, io.SeekStart); err != nil {
		return 0
	}
	version := make([]byte, 4)
	if _, err := io.ReadFull(file, version); err != nil {
		return 0
	}
	if version[0] == 0 {
		payload := make([]byte, 16)
		if _, err := io.ReadFull(file, payload); err != nil {
			return 0
		}
		timescale := binary.BigEndian.Uint32(payload[8:12])
		duration := binary.BigEndian.Uint32(payload[12:16])
		if timescale == 0 {
			return 0
		}
		return float64(duration) / float64(timescale)
	}
	if version[0] == 1 {
		payload := make([]byte, 28)
		if _, err := io.ReadFull(file, payload); err != nil {
			return 0
		}
		timescale := binary.BigEndian.Uint32(payload[16:20])
		duration := binary.BigEndian.Uint64(payload[20:28])
		if timescale == 0 {
			return 0
		}
		return float64(duration) / float64(timescale)
	}
	return 0
}

// findMP4Atom returns the payload bounds of a direct child atom.
func findMP4Atom(file *os.File, start, end int64, wanted string) (int64, int64, bool) {
	for cursor := start; cursor+8 <= end; {
		if _, err := file.Seek(cursor, io.SeekStart); err != nil {
			return 0, 0, false
		}
		header := make([]byte, 8)
		if _, err := io.ReadFull(file, header); err != nil {
			return 0, 0, false
		}
		size := int64(binary.BigEndian.Uint32(header[:4]))
		headerSize := int64(8)
		if size == 1 {
			extended := make([]byte, 8)
			if _, err := io.ReadFull(file, extended); err != nil {
				return 0, 0, false
			}
			unsigned := binary.BigEndian.Uint64(extended)
			if unsigned > math.MaxInt64 {
				return 0, 0, false
			}
			size = int64(unsigned)
			headerSize = 16
		} else if size == 0 {
			size = end - cursor
		}
		if size < headerSize || size > end-cursor {
			return 0, 0, false
		}
		if string(header[4:8]) == wanted {
			return cursor + headerSize, cursor + size, true
		}
		cursor += size
	}
	return 0, 0, false
}
