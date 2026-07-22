package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const captureSequenceFileFormat = 1

type captureSequenceFile struct {
	Format    int    `json:"format"`
	HighWater uint64 `json:"highWater"`
	Checksum  string `json:"checksum"`
}

func captureSequencePath(memoryPath string) string {
	memoryPath = strings.TrimSpace(memoryPath)
	if memoryPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(memoryPath), "meeting-capture-sequence.json")
}

func currentDurableCaptureSequence(memoryPath string, seed uint64) (uint64, error) {
	return mutateDurableCaptureSequence(memoryPath, seed, false)
}

func nextDurableCaptureSequence(memoryPath string, seed uint64) (uint64, error) {
	return mutateDurableCaptureSequence(memoryPath, seed, true)
}

func mutateDurableCaptureSequence(memoryPath string, seed uint64, advance bool) (uint64, error) {
	path := captureSequencePath(memoryPath)
	if path == "" {
		if advance {
			return seed + 1, nil
		}
		return seed, nil
	}
	lock, err := acquireAdmissionAnchorFileLock(path)
	if err != nil {
		return 0, fmt.Errorf("capture sequence lock: %w", err)
	}
	defer releaseAdmissionAnchorFileLock(lock)
	highWater, found, err := loadCaptureSequence(path)
	if err != nil {
		return 0, err
	}
	changed := !found
	if highWater < seed {
		highWater = seed
		changed = true
	}
	if advance {
		if highWater == ^uint64(0) {
			return 0, errors.New("capture sequence exhausted")
		}
		highWater++
		changed = true
	}
	if changed {
		if err := persistCaptureSequence(path, highWater); err != nil {
			return 0, err
		}
	}
	return highWater, nil
}

func loadCaptureSequence(path string) (uint64, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var disk captureSequenceFile
	if err := decoder.Decode(&disk); err != nil {
		return 0, false, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return 0, false, err
	}
	if disk.Format != captureSequenceFileFormat || disk.Checksum != captureSequenceChecksum(disk.HighWater) {
		return 0, false, errors.New("capture sequence checksum or format mismatch")
	}
	return disk.HighWater, true, nil
}

func persistCaptureSequence(path string, highWater uint64) error {
	disk := captureSequenceFile{Format: captureSequenceFileFormat, HighWater: highWater, Checksum: captureSequenceChecksum(highWater)}
	raw, err := json.Marshal(disk)
	if err != nil {
		return err
	}
	return writeFileAtomicallyDurable(path, raw, 0o600)
}

func captureSequenceChecksum(highWater uint64) string {
	sum := sha256.Sum256([]byte(strconv.FormatUint(highWater, 10)))
	return hex.EncodeToString(sum[:])
}

func entryCaptureSequence(entry meetingMemoryEntry) (uint64, bool) {
	if entry.Metadata == nil {
		return 0, false
	}
	value, err := strconv.ParseUint(strings.TrimSpace(entry.Metadata["captureSequence"]), 10, 64)
	return value, err == nil && value > 0
}

func maxPersistedCaptureSequence(entries []meetingMemoryEntry) uint64 {
	var highWater uint64
	for _, entry := range entries {
		if entry.Kind != meetingMemoryKindTranscript {
			continue
		}
		if sequence, ok := entryCaptureSequence(entry); ok && sequence > highWater {
			highWater = sequence
		}
	}
	return highWater
}
