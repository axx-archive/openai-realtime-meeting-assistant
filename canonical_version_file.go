package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
)

type FileCanonicalObjectVersionMap struct {
	mu     sync.Mutex
	path   string
	memory *MemoryCanonicalObjectVersionMap
}

type canonicalVersionFile struct {
	Format   int                           `json:"format"`
	Entries  []CanonicalObjectVersionEntry `json:"entries"`
	Checksum [32]byte                      `json:"checksum"`
}

var ErrCanonicalStaleVersionSnapshot = errors.New("canonical version snapshot does not extend durable state")

type canonicalVersionRequest struct {
	Family      string
	ObjectKey   string
	StateDigest string
}

type canonicalVersionResult struct {
	Version  int64
	Existing bool
}

func OpenFileCanonicalObjectVersionMap(path string) (*FileCanonicalObjectVersionMap, error) {
	if path == "" {
		return nil, errors.New("version map path required")
	}
	memory, err := loadCanonicalVersionMemory(path)
	if err != nil {
		return nil, err
	}
	return &FileCanonicalObjectVersionMap{path: path, memory: memory}, nil
}

func loadCanonicalVersionMemory(path string) (*MemoryCanonicalObjectVersionMap, error) {
	result := NewMemoryCanonicalObjectVersionMap()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var disk canonicalVersionFile
	if err := decoder.Decode(&disk); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if disk.Format != 1 {
		return nil, errors.New("unsupported canonical version map format")
	}
	checksum, err := checksumCanonicalVersionEntries(disk.Entries)
	if err != nil {
		return nil, err
	}
	if checksum != disk.Checksum {
		return nil, errors.New("canonical version map checksum mismatch")
	}
	seenVersion := make(map[string]map[int64]bool)
	for _, entry := range disk.Entries {
		if entry.Family == "" || entry.ObjectKey == "" || !isHexDigest(entry.StateDigest) || entry.Version < 1 {
			return nil, errors.New("invalid canonical version map entry")
		}
		object := entry.Family + "\x00" + entry.ObjectKey
		if seenVersion[object] == nil {
			seenVersion[object] = make(map[int64]bool)
		}
		if seenVersion[object][entry.Version] || result.versions[object][entry.StateDigest] != 0 {
			return nil, errors.New("duplicate canonical version map entry")
		}
		seenVersion[object][entry.Version] = true
		if result.versions[object] == nil {
			result.versions[object] = make(map[string]int64)
		}
		result.versions[object][entry.StateDigest] = entry.Version
		if entry.Version > result.max[object] {
			result.max[object] = entry.Version
		}
	}
	return result, nil
}

func (versionMap *FileCanonicalObjectVersionMap) ResolveVersion(family, objectKey, stateDigest string) (int64, bool, error) {
	// The file-backed implementation never exposes an in-memory-only
	// allocation: callers through the generic interface still get the durable
	// crash-safe contract.
	return versionMap.ResolveVersionDurably(context.Background(), family, objectKey, stateDigest)
}

// ResolveVersionDurably allocates and fsyncs a version as one logical
// operation. If persistence fails, the in-memory allocation is rolled back so
// a retry cannot observe a version that was never durable.
func (versionMap *FileCanonicalObjectVersionMap) ResolveVersionDurably(ctx context.Context, family, objectKey, stateDigest string) (int64, bool, error) {
	versionMap.mu.Lock()
	defer versionMap.mu.Unlock()
	lock, err := versionMap.acquireFileLock()
	if err != nil {
		return 0, false, err
	}
	defer releaseCanonicalFileLock(lock)
	// Reload after acquiring the process-shared lock. Independent instances
	// may have advanced this object since this handle was opened.
	latest, err := loadCanonicalVersionMemory(versionMap.path)
	if err != nil {
		return 0, false, err
	}
	versionMap.memory = latest
	before, err := snapshotCanonicalVersionMemory(versionMap.memory)
	if err != nil {
		return 0, false, err
	}
	version, existing, err := versionMap.memory.ResolveVersion(family, objectKey, stateDigest)
	if err != nil {
		return 0, false, err
	}
	after, err := snapshotCanonicalVersionMemory(versionMap.memory)
	if err != nil {
		versionMap.memory = memoryCanonicalVersionMapFromSnapshot(before)
		return 0, false, err
	}
	if err := versionMap.persistLocked(ctx, after); err != nil {
		versionMap.memory = memoryCanonicalVersionMapFromSnapshot(before)
		return 0, false, err
	}
	return version, existing, nil
}

// ResolveVersionsDurably resolves a deterministic import batch under one
// process/file lock and publishes at most one fsynced snapshot. First boot can
// contain thousands of legacy objects; rewriting the growing JSON map once per
// object is quadratic and keeps the HTTP listener offline for many minutes.
func (versionMap *FileCanonicalObjectVersionMap) ResolveVersionsDurably(ctx context.Context, requests []canonicalVersionRequest) ([]canonicalVersionResult, error) {
	versionMap.mu.Lock()
	defer versionMap.mu.Unlock()
	lock, err := versionMap.acquireFileLock()
	if err != nil {
		return nil, err
	}
	defer releaseCanonicalFileLock(lock)

	latest, err := loadCanonicalVersionMemory(versionMap.path)
	if err != nil {
		return nil, err
	}
	versionMap.memory = latest
	before, err := snapshotCanonicalVersionMemory(versionMap.memory)
	if err != nil {
		return nil, err
	}
	results := make([]canonicalVersionResult, len(requests))
	changed := false
	for index, request := range requests {
		select {
		case <-ctx.Done():
			versionMap.memory = memoryCanonicalVersionMapFromSnapshot(before)
			return nil, ctx.Err()
		default:
		}
		version, existing, resolveErr := versionMap.memory.ResolveVersion(request.Family, request.ObjectKey, request.StateDigest)
		if resolveErr != nil {
			versionMap.memory = memoryCanonicalVersionMapFromSnapshot(before)
			return nil, resolveErr
		}
		results[index] = canonicalVersionResult{Version: version, Existing: existing}
		changed = changed || !existing
	}
	if !changed {
		return results, nil
	}
	after, err := snapshotCanonicalVersionMemory(versionMap.memory)
	if err != nil {
		versionMap.memory = memoryCanonicalVersionMapFromSnapshot(before)
		return nil, err
	}
	if err := versionMap.persistLocked(ctx, after); err != nil {
		versionMap.memory = memoryCanonicalVersionMapFromSnapshot(before)
		return nil, err
	}
	return results, nil
}

func (versionMap *FileCanonicalObjectVersionMap) Snapshot() (CanonicalObjectVersionSnapshot, error) {
	return snapshotCanonicalVersionMemory(versionMap.memory)
}

func (versionMap *FileCanonicalObjectVersionMap) Persist(ctx context.Context, snapshot CanonicalObjectVersionSnapshot) error {
	versionMap.mu.Lock()
	defer versionMap.mu.Unlock()
	lock, err := versionMap.acquireFileLock()
	if err != nil {
		return err
	}
	defer releaseCanonicalFileLock(lock)
	latest, err := loadCanonicalVersionMemory(versionMap.path)
	if err != nil {
		return err
	}
	latestSnapshot, err := snapshotCanonicalVersionMemory(latest)
	if err != nil {
		return err
	}
	if err := ensureCanonicalVersionSnapshotExtends(latestSnapshot.Entries, snapshot.Entries); err != nil {
		return err
	}
	return versionMap.persistLocked(ctx, snapshot)
}

func (versionMap *FileCanonicalObjectVersionMap) persistLocked(ctx context.Context, snapshot CanonicalObjectVersionSnapshot) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	checksum, err := checksumCanonicalVersionEntries(snapshot.Entries)
	if err != nil {
		return err
	}
	if checksum != snapshot.Checksum {
		return errors.New("canonical version snapshot checksum mismatch")
	}
	disk := canonicalVersionFile{Format: 1, Entries: snapshot.Entries, Checksum: snapshot.Checksum}
	// This is a fixed-field persistence envelope, not a cross-language hash
	// input. Its independently verified checksum covers canonically ordered
	// entries; ordinary compact JSON avoids special handling for a nil slice.
	data, err := json.Marshal(disk)
	if err != nil {
		return err
	}
	dir := filepath.Dir(versionMap.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".canonical-version-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, versionMap.path); err != nil {
		return err
	}
	return syncCanonicalParentDir(versionMap.path)
}

func (versionMap *FileCanonicalObjectVersionMap) PersistCurrent(ctx context.Context) error {
	snapshot, err := versionMap.Snapshot()
	if err != nil {
		return err
	}
	return versionMap.Persist(ctx, snapshot)
}

func checksumCanonicalVersionEntries(entries []CanonicalObjectVersionEntry) ([32]byte, error) {
	if entries == nil {
		entries = []CanonicalObjectVersionEntry{}
	}
	data, err := canonicalJSON(entries)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(data), nil
}

func ensureCanonicalVersionSnapshotExtends(latest, candidate []CanonicalObjectVersionEntry) error {
	type objectVersion struct {
		object  string
		version int64
	}
	candidateByDigest := make(map[string]int64, len(candidate))
	candidateByVersion := make(map[objectVersion]string, len(candidate))
	for _, entry := range candidate {
		if entry.Family == "" || entry.ObjectKey == "" || !isHexDigest(entry.StateDigest) || entry.Version < 1 {
			return fmt.Errorf("%w: invalid candidate entry", ErrCanonicalStaleVersionSnapshot)
		}
		object := entry.Family + "\x00" + entry.ObjectKey
		digestKey := object + "\x00" + entry.StateDigest
		if _, exists := candidateByDigest[digestKey]; exists {
			return fmt.Errorf("%w: duplicate state digest", ErrCanonicalStaleVersionSnapshot)
		}
		versionKey := objectVersion{object: object, version: entry.Version}
		if _, exists := candidateByVersion[versionKey]; exists {
			return fmt.Errorf("%w: aggregate version collision", ErrCanonicalStaleVersionSnapshot)
		}
		candidateByDigest[digestKey] = entry.Version
		candidateByVersion[versionKey] = entry.StateDigest
	}
	for _, entry := range latest {
		object := entry.Family + "\x00" + entry.ObjectKey
		digestKey := object + "\x00" + entry.StateDigest
		if version, exists := candidateByDigest[digestKey]; !exists || version != entry.Version {
			return fmt.Errorf("%w: durable mapping missing or changed", ErrCanonicalStaleVersionSnapshot)
		}
	}
	return nil
}

func snapshotCanonicalVersionMemory(memory *MemoryCanonicalObjectVersionMap) (CanonicalObjectVersionSnapshot, error) {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	entries := make([]CanonicalObjectVersionEntry, 0)
	for object, digests := range memory.versions {
		parts := strings.SplitN(object, "\x00", 2)
		for digest, version := range digests {
			entries = append(entries, CanonicalObjectVersionEntry{Family: parts[0], ObjectKey: parts[1], StateDigest: digest, Version: version})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Family != entries[j].Family {
			return entries[i].Family < entries[j].Family
		}
		if entries[i].ObjectKey != entries[j].ObjectKey {
			return entries[i].ObjectKey < entries[j].ObjectKey
		}
		return entries[i].StateDigest < entries[j].StateDigest
	})
	checksum, err := checksumCanonicalVersionEntries(entries)
	if err != nil {
		return CanonicalObjectVersionSnapshot{}, err
	}
	return CanonicalObjectVersionSnapshot{Entries: entries, Checksum: checksum}, nil
}

func memoryCanonicalVersionMapFromSnapshot(snapshot CanonicalObjectVersionSnapshot) *MemoryCanonicalObjectVersionMap {
	memory := NewMemoryCanonicalObjectVersionMap()
	for _, entry := range snapshot.Entries {
		object := entry.Family + "\x00" + entry.ObjectKey
		if memory.versions[object] == nil {
			memory.versions[object] = make(map[string]int64)
		}
		memory.versions[object][entry.StateDigest] = entry.Version
		if entry.Version > memory.max[object] {
			memory.max[object] = entry.Version
		}
	}
	return memory
}

func syncCanonicalParentDir(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync canonical parent directory: %w", err)
	}
	return nil
}

func (versionMap *FileCanonicalObjectVersionMap) acquireFileLock() (*os.File, error) {
	dir := filepath.Dir(versionMap.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lockPath := versionMap.path + ".lock"
	_, statErr := os.Stat(lockPath)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return nil, statErr
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if created {
		if err := syncCanonicalParentDir(lockPath); err != nil {
			lock.Close()
			return nil, err
		}
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		lock.Close()
		return nil, err
	}
	return lock, nil
}

func releaseCanonicalFileLock(lock *os.File) {
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = lock.Close()
}
